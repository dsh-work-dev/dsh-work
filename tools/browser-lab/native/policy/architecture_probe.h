// Isolated research fixture, deliberately not a product resource service.
// Built-in HTTP origins avoid unproved custom-scheme storage semantics. The
// per-context factory handles *every* HTTP request, denying foreign leases.
#ifndef BROWSER_LAB_ARCHITECTURE_PROBE_H_
#define BROWSER_LAB_ARCHITECTURE_PROBE_H_
#include "include/cef_request_context.h"
#include "include/cef_resource_handler.h"
#include "include/cef_scheme.h"
#include "include/cef_parser.h"
#include "include/base/cef_bind.h"
#include "include/base/cef_callback.h"
#include "include/wrapper/cef_closure_task.h"
#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cstdint>
#include <string>
namespace lab_arch {
inline bool Enabled() { return std::getenv("BROWSER_LAB_ARCH_ARTIFACT") != nullptr; }
inline std::string Nonce() {
  const auto root = std::getenv("BROWSER_LAB_DATA_ROOT");
  if (root) {
    FILE* file = std::fopen((std::string(root) + "/arch-current-lease").c_str(), "rb");
    if (file) {
      char value[65] = {};
      const auto n = std::fread(value, 1, 64, file); std::fclose(file);
      const std::string nonce(value, n);
      if (n == 32 && nonce.find_first_not_of("0123456789abcdef") == std::string::npos) return nonce;
    }
  }
  const auto p = std::getenv("BROWSER_LAB_ARCH_NONCE");
  return p ? p : "missing-nonce";
}
inline std::string Origin(int i) {
  return "http://" + std::string(i ? "b." : "a.") + Nonce() + ".localhost";
}
inline std::string URL(int i) { return Origin(i) + "/?tab=" + (i ? "B" : "A"); }
class Resource final : public CefResourceHandler {
  std::string body_, mime_, marker_;
  int status_;
  size_t offset_ = 0;
  bool slow_ = false, canceled_ = false;
  bool Revoked() {
    if (marker_.empty()) return false;
    FILE* file = std::fopen(marker_.c_str(), "rb");
    if (!file) return false;
    std::fclose(file); return true;
  }
public:
  Resource(std::string body, std::string mime, int status, std::string marker = "", bool slow = false)
      : body_(std::move(body)), mime_(std::move(mime)), marker_(std::move(marker)), status_(status), slow_(slow) {}
  bool Open(CefRefPtr<CefRequest>, bool& handle, CefRefPtr<CefCallback>) override {
    handle = true; return true;
  }
  void GetResponseHeaders(CefRefPtr<CefResponse> r, int64_t& length, CefString&) override {
    r->SetStatus(status_); r->SetMimeType(mime_);
    CefResponse::HeaderMap h;
    h.emplace("Cache-Control", "no-store");
    h.emplace("Content-Security-Policy", "worker-src 'none'; frame-src 'self'; object-src 'none'");
    r->SetHeaderMap(h); length = static_cast<int64_t>(body_.size());
  }
  bool Read(void* data, int size, int& read, CefRefPtr<CefResourceReadCallback> callback) override {
    if (Revoked()) { read = -2; return false; }
    if (slow_ && offset_ < body_.size()) {
      read = 0;
      CefPostDelayedTask(TID_IO, base::BindOnce(
        [](CefRefPtr<Resource> self, uintptr_t out, int size, CefRefPtr<CefResourceReadCallback> callback) {
          if (self->canceled_) return;
          if (self->Revoked()) { callback->Continue(-2); return; }
          const int n = static_cast<int>(std::min({static_cast<size_t>(size), size_t(4096), self->body_.size() - self->offset_}));
          std::memcpy(reinterpret_cast<void*>(out), self->body_.data() + self->offset_, n);
          self->offset_ += n; callback->Continue(n);
        }, CefRefPtr<Resource>(this), reinterpret_cast<uintptr_t>(data), size, callback), 100);
      return true;
    }
    read = static_cast<int>(std::min(static_cast<size_t>(size), body_.size() - offset_));
    if (read) std::memcpy(data, body_.data() + offset_, read);
    offset_ += read; return read > 0;
  }
  void Cancel() override { canceled_ = true; }
private:
  IMPLEMENT_REFCOUNTING(Resource);
};
class Factory final : public CefSchemeHandlerFactory {
  int index_;
  std::string nonce_, origin_;
public:
  explicit Factory(int index) : index_(index), nonce_(Nonce()), origin_(Origin(index)) {}
  CefRefPtr<CefResourceHandler> Create(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>,
                                      const CefString&, CefRefPtr<CefRequest> request) override {
    const std::string url = request->GetURL();
    const std::string prefix = origin_ + "/";
    const auto root = std::getenv("BROWSER_LAB_DATA_ROOT");
    const std::string marker = std::string(root ? root : "") + "/arch-revoke-" + (index_ ? "B-" : "A-") + nonce_;
    FILE* file = std::fopen(marker.c_str(), "rb");
    const bool revoked = file != nullptr;
    if (file) std::fclose(file);
    if (revoked || url.compare(0, prefix.size(), prefix) != 0)
      return new Resource("LEASE_DENIED", "text/plain", 403);
    const auto path = url.substr(prefix.size());
    if (path == "worker.js") return new Resource("self.onmessage=()=>postMessage('unexpected-worker');", "text/javascript", 200);
    if (path == "sw.js") return new Resource("self.addEventListener('fetch',e=>e.respondWith(new Response('HIJACK')));", "text/javascript", 200);
    if (path == "asset.js") return new Resource("window.relativeAsset=true;", "text/javascript", 200);
    if (path == "secret") return new Resource(index_ ? "ROOT_B_ONLY" : "ROOT_A_ONLY", "text/plain", 200);
    if (path == "slow") return new Resource(std::string(262144, 'x'), "text/plain", 200, marker, true);
    const std::string html = "<!doctype html><meta charset=utf-8><title>Browser Lab Tab " + std::string(index_ ? "B" : "A") + "</title>"
      "<body style='background:#edf0f5'><i style='display:block;width:16px;height:16px;background:#ed60a5'></i>"
      "<h1>Isolated artifact</h1><input id=entry><script src=asset.js></script>"
      "<script>window.lab={instance:crypto.randomUUID()};window.memoryOnly=crypto.randomUUID()</script>";
    return new Resource(html, "text/html", 200);
  }
private:
  IMPLEMENT_REFCOUNTING(Factory);
};
inline CefRefPtr<CefRequestContext> Context(int i) {
  if (!Enabled()) return nullptr;
  CefRequestContextSettings settings; // empty cache_path: memory-only
  auto context = CefRequestContext::CreateContext(settings, nullptr);
  context->RegisterSchemeHandlerFactory("http", "", new Factory(i));
  // A foreign HTTPS document must not enter this temporary partition and
  // escape its Worker policy. The fixture denies it without network access.
  context->RegisterSchemeHandlerFactory("https", "", new Factory(i));
  // Hold the first context only until the second is made, not past Shutdown.
  static CefRefPtr<CefRequestContext> first;
  if (i == 0) first = context;
  else {
    std::fprintf(stderr, "ARCH_CONTEXT sharing=%d cache_empty=%d global_sharing=%d\n",
      first && context->IsSharingWith(first) ? 1 : 0,
      context->GetCachePath().empty() ? 1 : 0,
      context->IsSharingWith(CefRequestContext::GetGlobalContext()) ? 1 : 0);
    first = nullptr;
  }
  return context;
}
} // namespace lab_arch
#endif
