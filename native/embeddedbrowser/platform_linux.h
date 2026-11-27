#pragma once
#include "presentation_linux.h"
#include <cerrno>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <openssl/crypto.h>
#include <openssl/hmac.h>
#include <sys/socket.h>
#include <sys/un.h>

namespace dsh_platform {
using Window = CefWindowHandle;
inline int pipe_handle = -1, host_pidfd = -1;
inline bool Transfer(bool write, void *bytes, uint32_t size) {
  auto *p = static_cast<char *>(bytes);
  while (size) {
    auto count = write ? send(pipe_handle, p, size, MSG_NOSIGNAL)
                       : recv(pipe_handle, p, size, 0);
    if (count < 0 && errno == EINTR)
      continue;
    if (count <= 0)
      return false;
    p += count;
    size -= static_cast<uint32_t>(count);
  }
  return true;
}
inline std::string TakeEnvironment(const char *key) {
  auto *value = getenv(key);
  std::string result = value ? value : "";
  unsetenv(key);
  return result;
}
inline bool OpenTransport(const std::string &path) {
  if (path.empty() || path.size() >= sizeof(sockaddr_un::sun_path))
    return false;
  auto pidText = TakeEnvironment("DSH_WORK_CEF_HOST_PID"),
       birthText = TakeEnvironment("DSH_WORK_CEF_HOST_STARTED");
  uint32_t expected = 0;
  uint64_t birth = 0;
  if (!dsh_x11::number(pidText, expected) ||
      !dsh_x11::number(birthText, birth)) {
    return false;
  }
  pipe_handle = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
  if (pipe_handle < 0)
    return false;
  sockaddr_un address{};
  address.sun_family = AF_UNIX;
  std::memcpy(address.sun_path, path.c_str(), path.size() + 1);
  if (connect(pipe_handle, reinterpret_cast<sockaddr *>(&address),
              sizeof(address)))
    return false;
  ucred peer{};
  socklen_t length = sizeof(peer);
  if (getsockopt(pipe_handle, SOL_SOCKET, SO_PEERCRED, &peer, &length) ||
      peer.uid != geteuid() || peer.pid != static_cast<pid_t>(expected))
    return false;
#ifndef SO_PEERPIDFD
#define SO_PEERPIDFD 77
#endif
  length = sizeof(host_pidfd);
  if (getsockopt(pipe_handle, SOL_SOCKET, SO_PEERPIDFD, &host_pidfd, &length))
    return false;
  if (fcntl(host_pidfd, F_SETFD, FD_CLOEXEC) < 0)
    return false;
  return host_pidfd >= 0 &&
         syscall(SYS_pidfd_send_signal, host_pidfd, 0, nullptr, 0) == 0 &&
         dsh_x11::started(expected) == birth &&
         syscall(SYS_pidfd_send_signal, host_pidfd, 0, nullptr, 0) == 0;
}
inline bool Authenticate(const std::string &token) {
  std::array<unsigned char, 32> nonce{}, actual{}, expected{};
  if (!Transfer(false, nonce.data(), nonce.size()))
    return false;
  auto mac = [&](const std::string &role, std::array<unsigned char, 32> &out) {
    std::string data = role;
    data.append(reinterpret_cast<char *>(nonce.data()), nonce.size());
    unsigned int size = 0;
    return HMAC(EVP_sha256(), token.data(), static_cast<int>(token.size()),
                reinterpret_cast<const unsigned char *>(data.data()),
                data.size(), out.data(), &size) &&
           size == out.size();
  };
  return mac("worker", expected) && Transfer(true, expected.data(), 32) &&
         Transfer(false, actual.data(), 32) && mac("host", expected) &&
         CRYPTO_memcmp(actual.data(), expected.data(), 32) == 0;
}
inline void Erase(std::string &value) {
  OPENSSL_cleanse(value.data(), value.size());
  value.clear();
}
inline void CancelTransport() {
  if (pipe_handle >= 0)
    shutdown(pipe_handle, SHUT_RDWR);
}
inline void CloseTransport() {
  if (pipe_handle >= 0) {
    close(pipe_handle);
    pipe_handle = -1;
  }
  if (host_pidfd >= 0) {
    close(host_pidfd);
    host_pidfd = -1;
  }
}
inline bool Visible(Window w) { return dsh_x11::visible(w); }
inline std::string Hide(Window w) { return dsh_x11::hide(w); }
inline std::string Present(Window hwnd, CefRefPtr<CefDictionaryValue> args) {
  auto fail = [&]() {
    auto hidden = Hide(hwnd);
    return std::string("CEF_PRESENT_ARGUMENTS_REJECTED") +
           (hidden.empty() ? "" : " / " + hidden);
  };
  if (!args)
    return fail();
  uint64_t parent = 0, birth = 0;
  if (!dsh_x11::number(args->GetString("parent").ToString(), parent) ||
      !dsh_x11::number(args->GetString("started").ToString(), birth)) {
    return fail();
  }
  auto box = args->GetDictionary("bounds");
  if (!box || args->GetString("view").empty() ||
      args->GetString("epoch").empty())
    return fail();
  return dsh_x11::attach(hwnd, parent,
                         static_cast<uint32_t>(args->GetInt("pid")), birth,
                         box->GetInt("X"), box->GetInt("Y"),
                         box->GetInt("Width"), box->GetInt("Height"),
                         args->GetBool("visible") && box->GetInt("Width") > 0 &&
                             box->GetInt("Height") > 0);
}
inline bool ConfigureWindow(CefWindowInfo &window) {
  auto parent = dsh_x11::parking();
  if (!parent)
    return false;
  window.SetAsChild(parent, CefRect(0, 0, 1, 1));
  window.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
  return true;
}
} // namespace dsh_platform
