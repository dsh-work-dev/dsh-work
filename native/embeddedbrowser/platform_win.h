#pragma once
#include "presentation_window.h"
#include <bcrypt.h>
#include <windows.h>
#include <condition_variable>
#include <deque>
namespace dsh_platform {
using Window = CefWindowHandle;
inline HANDLE pipe_handle = INVALID_HANDLE_VALUE;
inline HANDLE transport_stop = nullptr;
inline std::mutex transport_mutex;
inline std::condition_variable transport_changed;
struct OutboundFrame { std::string key, bytes; };
inline std::deque<OutboundFrame> transport_queue;
inline size_t transport_bytes = 0;
inline bool transport_closed = false;
inline std::thread transport_writer;
// A truncated frame invalidates this authenticated stream. Process retirement
// closes all writer handles; Host cleanup still requires its kernel/tree join.
[[noreturn]] inline void RetireTransport() {
  TerminateProcess(GetCurrentProcess(), 74);
  ExitProcess(74);
}
bool Transfer(bool write, void *bytes, DWORD size) {
  auto *p = static_cast<char *>(bytes);
  while (size) {
    DWORD n = 0;
    OVERLAPPED operation = {};
    operation.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (!operation.hEvent) return false;
    bool ok = write ? !!WriteFile(pipe_handle, p, size, &n, &operation)
                    : !!ReadFile(pipe_handle, p, size, &n, &operation);
    if (!ok && GetLastError() == ERROR_IO_PENDING) {
      HANDLE events[] = {operation.hEvent, transport_stop};
      // Only the dedicated sender waits for outbound capacity. No CEF or
      // download cancellation thread waits on Host pipe reads.
      DWORD wait = WaitForMultipleObjects(transport_stop ? 2 : 1, events, FALSE,
                                         write ? 10000 : INFINITE);
      if (wait != WAIT_OBJECT_0) {
        CancelIoEx(pipe_handle, &operation);
        if (WaitForSingleObject(operation.hEvent, 1000) != WAIT_OBJECT_0)
          RetireTransport(); // never return with an in-flight stack OVERLAPPED
        GetOverlappedResult(pipe_handle, &operation, &n, FALSE);
        CloseHandle(operation.hEvent);
        if (write && wait == WAIT_TIMEOUT) RetireTransport();
        return false;
      }
      ok = !!GetOverlappedResult(pipe_handle, &operation, &n, FALSE);
    }
    CloseHandle(operation.hEvent);
    if (!ok || !n) return false;
    p += n;
    size -= n;
  }
  return true;
}
inline void QueueFrame(std::string json, std::string key) {
  uint32_t size = static_cast<uint32_t>(json.size());
  std::string frame(reinterpret_cast<const char*>(&size), sizeof(size));
  frame += json;
  std::lock_guard<std::mutex> lock(transport_mutex);
  if (transport_closed) return;
  constexpr size_t capacity=16u<<20;
  if(frame.size()>capacity)RetireTransport();
  // Latest progress is replaceable; terminal/control messages never are.
  if (!key.empty()) for (auto& queued : transport_queue) if (queued.key == key) {
    const auto remaining=transport_bytes-queued.bytes.size();
    if(remaining>capacity-frame.size())RetireTransport();
    queued.bytes = std::move(frame); transport_bytes=remaining+queued.bytes.size();
    transport_changed.notify_one(); return;
  }
  if (transport_queue.size() >= 256 || transport_bytes > capacity-frame.size())
    RetireTransport();
  transport_bytes += frame.size();
  transport_queue.push_back({std::move(key),std::move(frame)});
  transport_changed.notify_one();
}
inline void StartTransportWriter() {
  transport_writer=std::thread([] {
    for (;;) {
      OutboundFrame frame;
      {
        std::unique_lock<std::mutex> lock(transport_mutex);
        transport_changed.wait(lock,[]{return transport_closed||!transport_queue.empty();});
        if (transport_closed) return;
        frame=std::move(transport_queue.front());transport_queue.pop_front();
        transport_bytes-=frame.bytes.size();
      }
      if (!Transfer(true,frame.bytes.data(),static_cast<DWORD>(frame.bytes.size()))) {
        if (WaitForSingleObject(transport_stop,0)==WAIT_OBJECT_0) return;
        RetireTransport();
      }
    }
  });
}
std::string TakeEnvironment(const char *input) {
  auto wide = CefString(input).ToWString();
  const wchar_t *name = wide.c_str();
  const DWORD size = GetEnvironmentVariableW(name, nullptr, 0);
  if (!size)
    return {};
  std::wstring value(size, L'\0');
  GetEnvironmentVariableW(name, value.data(), size);
  SetEnvironmentVariableW(name, nullptr);
  value.resize(size - 1);
  return CefString(value).ToString();
}
bool Authenticate(const std::string &token) {
  std::array<unsigned char, 32> nonce, actual, expected;
  if (!Transfer(false, nonce.data(), 32))
    return false;
  BCRYPT_ALG_HANDLE alg = nullptr;
  if (BCryptOpenAlgorithmProvider(&alg, BCRYPT_SHA256_ALGORITHM, nullptr,
                                  BCRYPT_ALG_HANDLE_HMAC_FLAG) < 0)
    return false;
  auto mac = [&](const char *role, size_t size,
                 std::array<unsigned char, 32> &out) {
    BCRYPT_HASH_HANDLE hash = nullptr;
    bool ok = BCryptCreateHash(
                  alg, &hash, nullptr, 0,
                  reinterpret_cast<PUCHAR>(const_cast<char *>(token.data())),
                  static_cast<ULONG>(token.size()), 0) >= 0;
    if (ok)
      ok = BCryptHashData(hash,
                          reinterpret_cast<PUCHAR>(const_cast<char *>(role)),
                          static_cast<ULONG>(size), 0) >= 0;
    if (ok)
      ok = BCryptHashData(hash, nonce.data(), 32, 0) >= 0;
    if (ok)
      ok = BCryptFinishHash(hash, out.data(), 32, 0) >= 0;
    if (hash)
      BCryptDestroyHash(hash);
    return ok;
  };
  bool ok = mac("worker", 6, expected) && Transfer(true, expected.data(), 32) &&
            Transfer(false, actual.data(), 32) && mac("host", 4, expected);
  unsigned char difference = 0;
  for (size_t i = 0; i < 32; ++i)
    difference |= actual[i] ^ expected[i];
  BCryptCloseAlgorithmProvider(alg, 0);
  return ok && !difference;
}

inline bool OpenTransport(const std::string &path) {
  auto wide = CefString(path).ToWString();
  pipe_handle =
      CreateFileW(wide.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr,
                  OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
  if (pipe_handle == INVALID_HANDLE_VALUE) return false;
  transport_stop=CreateEventW(nullptr,TRUE,FALSE,nullptr);
  return transport_stop != nullptr;
}
inline void Erase(std::string &token) {
  SecureZeroMemory(token.data(), token.size());
  token.clear();
}
inline void CancelTransport() {
  {std::lock_guard<std::mutex> lock(transport_mutex);transport_closed=true;}
  if(transport_stop)SetEvent(transport_stop);
  transport_changed.notify_all();CancelIoEx(pipe_handle,nullptr);
}
inline void CloseTransport() {
  CancelTransport();if(transport_writer.joinable())transport_writer.join();
  CloseHandle(pipe_handle);if(transport_stop)CloseHandle(transport_stop);
}
inline bool Visible(Window hwnd) {
  return IsWindow(hwnd) && IsWindowVisible(hwnd) &&
         !IsIconic(GetAncestor(hwnd, GA_ROOT));
}
inline std::string Hide(Window hwnd) {
  dsh_presenter::WindowOps ops;
  return dsh_presenter::hide(ops, hwnd);
}
inline std::string Present(Window hwnd, CefRefPtr<CefDictionaryValue> args) {
  dsh_presenter::WindowOps ops;
  auto fail = [&](const std::string &code) {
    auto hidden = dsh_presenter::hide(ops, hwnd);
    return code + (hidden.empty() ? "" : " / " + hidden);
  };
  if (!args)
    return fail("CEF_PRESENT_ARGUMENTS_REJECTED");
  HWND parent = nullptr;
  uint64_t started = 0;
  try {
    parent = reinterpret_cast<HWND>(
        std::stoull(args->GetString("parent").ToString()));
    started = std::stoull(args->GetString("started").ToString());
  } catch (...) {
    return fail("CEF_PRESENT_ARGUMENTS_REJECTED");
  }
  DWORD pid = static_cast<DWORD>(args->GetInt("pid"));
  auto process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
  if (!process)
    return fail("CEF_PRESENT_PROCESS_INSTANCE_REJECTED");
  FILETIME created, exited, kernel, user;
  bool same = GetProcessTimes(process, &created, &exited, &kernel, &user) &&
              ((static_cast<uint64_t>(created.dwHighDateTime) << 32) |
               created.dwLowDateTime) == started;
  if (!same) {
    CloseHandle(process);
    return fail("CEF_PRESENT_PROCESS_INSTANCE_REJECTED");
  }
  auto bounds = args->GetDictionary("bounds");
  RECT client{};
  if (!bounds || args->GetString("view").empty() ||
      args->GetString("epoch").empty() || !GetClientRect(parent, &client)) {
    CloseHandle(process);
    return fail("CEF_PRESENT_ARGUMENTS_REJECTED");
  }
  int x = bounds->GetInt("X"), y = bounds->GetInt("Y"),
      width = bounds->GetInt("Width"), height = bounds->GetInt("Height");
  if (x < 0 || y < 0 || width < 0 || height < 0 || x > client.right ||
      y > client.bottom || width > client.right - x ||
      height > client.bottom - y) {
    CloseHandle(process);
    return fail("CEF_PRESENT_BOUNDS_REJECTED");
  }
  bool visible = args->GetBool("visible") && width > 0 && height > 0;
  auto error = dsh_presenter::attach(ops, hwnd, parent, pid, x, y, width,
                                     height, visible);
  CloseHandle(process);
  if (!error.empty())
    return error;

  return {};
}
inline void DestroyOwnedGuest(Window expected) {
  DWORD pid = 0;
  if (IsWindow(expected) && GetWindowThreadProcessId(expected, &pid) &&
      pid == GetCurrentProcessId())
    DestroyWindow(expected);
}
inline bool ConfigureWindow(CefWindowInfo &window) {
  window.SetAsPopup(nullptr, "DSH Browser");
  window.style &= ~WS_VISIBLE;
  window.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
  window.ex_style |= WS_EX_NOACTIVATE;
  return true;
}
} // namespace dsh_platform
