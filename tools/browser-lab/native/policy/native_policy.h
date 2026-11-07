// Lab-only native policy. Uses CEF's maintained URLRequest transport; foreign
// renderers never receive the control token or access to callback objects.
#ifndef BROWSER_LAB_NATIVE_POLICY_H_
#define BROWSER_LAB_NATIVE_POLICY_H_
#include "include/cef_client.h"
#include "include/cef_parser.h"
#include "include/cef_urlrequest.h"
#include "include/wrapper/cef_helpers.h"
#include <cstdio>
#include <cstdlib>
#include <functional>
#include <map>
#include <string>
namespace browser_lab {
using Dictionary = CefRefPtr<CefDictionaryValue>;
class Rpc final : public CefURLRequestClient {
  inline static std::string token_;
  CefRefPtr<CefURLRequest> request_;
  std::string bytes_;
  std::function<void(Dictionary)> done_;

public:
  static void Initialize() {
    const char *value = getenv("BROWSER_LAB_TOKEN");
    token_ = value ? value : "";
#if defined(OS_WIN)
    _putenv_s("BROWSER_LAB_TOKEN", "");
    SetEnvironmentVariableA("BROWSER_LAB_TOKEN", nullptr);
#else
    unsetenv("BROWSER_LAB_TOKEN");
#endif
  }
  explicit Rpc(std::function<void(Dictionary)> done) : done_(std::move(done)) {}
  static void Send(const std::string &path, Dictionary body,
                   std::function<void(Dictionary)> done) {
    if (token_.empty()) {
      done(nullptr);
      return;
    }
    auto req = CefRequest::Create();
    req->SetURL("http://127.0.0.1:19483/native-policy/" + path);
    req->SetMethod("POST");
    req->SetHeaderByName("X-Browser-Lab-Token", token_, true);
    req->SetHeaderByName("Content-Type", "application/json", true);
    auto value = CefValue::Create();
    value->SetDictionary(body);
    auto json = CefWriteJSON(value, JSON_WRITER_DEFAULT).ToString();
    auto data = CefPostData::Create();
    auto element = CefPostDataElement::Create();
    element->SetToBytes(json.size(), json.data());
    data->AddElement(element);
    req->SetPostData(data);
    req->SetFlags(UR_FLAG_SKIP_CACHE | UR_FLAG_STOP_ON_REDIRECT);
    CefRefPtr<Rpc> client = new Rpc(std::move(done));
    client->request_ = CefURLRequest::Create(req, client, nullptr);
    if (!client->request_)
      client->done_(nullptr);
  }
  void OnRequestComplete(CefRefPtr<CefURLRequest> r) override {
    if (r->GetRequestStatus() != UR_SUCCESS || !r->GetResponse() ||
        r->GetResponse()->GetStatus() != 200)
      fprintf(stderr,
              "Browser Lab native policy RPC status=%d http=%d error=%d\n",
              static_cast<int>(r->GetRequestStatus()),
              r->GetResponse() ? r->GetResponse()->GetStatus() : 0,
              static_cast<int>(r->GetRequestError()));
    Dictionary result;
    if (r->GetRequestStatus() == UR_SUCCESS && r->GetResponse() &&
        r->GetResponse()->GetStatus() == 200) {
      auto value = CefParseJSON(bytes_, JSON_PARSER_RFC);
      if (value)
        result = value->GetDictionary();
    }
    auto done = std::move(done_);
    request_ = nullptr;
    done(result);
  }
  void OnUploadProgress(CefRefPtr<CefURLRequest>, int64_t, int64_t) override {}
  void OnDownloadProgress(CefRefPtr<CefURLRequest>, int64_t, int64_t) override {
  }
  void OnDownloadData(CefRefPtr<CefURLRequest> r, const void *data,
                      size_t n) override {
    if (bytes_.size() + n > 65536) {
      r->Cancel();
      return;
    }
    bytes_.append(static_cast<const char *>(data), n);
  }
  bool GetAuthCredentials(bool, const CefString &, int, const CefString &,
                          const CefString &,
                          CefRefPtr<CefAuthCallback>) override {
    return false;
  }

private:
  IMPLEMENT_REFCOUNTING(Rpc);
};
class AuthorizedDownload final : public CefURLRequestClient {
  CefRefPtr<CefURLRequest> request_;
  FILE *file_ = nullptr;
  std::string path_, id_;
  size_t bytes_ = 0;
  bool done_ = false;
  bool owns_partial_ = false;

public:
  static void Start(CefRefPtr<CefBrowser> browser, const std::string &url,
                    Dictionary decision) {
    CefRefPtr<AuthorizedDownload> client = new AuthorizedDownload();
    client->path_ = decision->GetString("path").ToString();
    client->id_ = decision->GetString("id").ToString();
    if (client->path_.empty() || client->id_.empty())
      return;
    client->file_ = fopen((client->path_ + ".partial").c_str(), "wbx");
    if (!client->file_) {
      client->Finish("destination-refused");
      return;
    }
    client->owns_partial_ = true;
    auto req = CefRequest::Create();
    req->SetURL(url);
    req->SetMethod("GET");
    req->SetFlags(UR_FLAG_SKIP_CACHE | UR_FLAG_STOP_ON_REDIRECT |
                  UR_FLAG_ALLOW_STORED_CREDENTIALS);
    client->request_ = CefURLRequest::Create(
        req, client, browser->GetHost()->GetRequestContext());
    if (!client->request_) {
      client->Finish("failed");
      return;
    }
    client->Watch();
  }
  void Watch() {
    auto body = CefDictionaryValue::Create();
    body->SetString("id", id_);
    CefRefPtr<AuthorizedDownload> self = this;
    Rpc::Send("watch", body, [self](Dictionary result) {
      if (self->done_)
        return;
      if (!result || result->GetBool("revoked")) {
        if (self->request_)
          self->request_->Cancel();
      } else
        self->Watch();
    });
  }
  void Finish(const std::string &state) {
    if (done_)
      return;
    done_ = true;
    if (file_) {
      fclose(file_);
      file_ = nullptr;
    }
    std::string actual = state;
    if (actual == "complete" &&
        rename((path_ + ".partial").c_str(), path_.c_str()) != 0)
      actual = "destination-refused";
    if (actual != "complete" && owns_partial_)
      remove((path_ + ".partial").c_str());
    request_ = nullptr;
    auto result = CefDictionaryValue::Create();
    result->SetString("id", id_);
    result->SetString("state", actual);
    result->SetInt("bytes", static_cast<int>(bytes_));
    Rpc::Send("result", result, [](Dictionary) {});
  }
  void OnRequestComplete(CefRefPtr<CefURLRequest> r) override {
    auto response = r->GetResponse();
    Finish(r->GetRequestStatus() == UR_SUCCESS && response &&
                   response->GetStatus() == 200
               ? "complete"
               : "canceled");
  }
  void OnUploadProgress(CefRefPtr<CefURLRequest>, int64_t, int64_t) override {}
  void OnDownloadProgress(CefRefPtr<CefURLRequest>, int64_t, int64_t) override {
  }
  void OnDownloadData(CefRefPtr<CefURLRequest> r, const void *data,
                      size_t n) override {
    if (!file_ || bytes_ + n > 8 * 1024 * 1024 ||
        fwrite(data, 1, n, file_) != n) {
      r->Cancel();
      return;
    }
    bytes_ += n;
  }
  bool GetAuthCredentials(bool, const CefString &, int, const CefString &,
                          const CefString &,
                          CefRefPtr<CefAuthCallback>) override {
    return false;
  }

private:
  IMPLEMENT_REFCOUNTING(AuthorizedDownload);
};
class NativePolicy final : public CefRequestHandler,
                           public CefPermissionHandler,
                           public CefDownloadHandler {
  struct Document {
    int index = 0;
    uint64_t epoch = 0;
  };
  std::map<int, Document> documents_;

public:
  static void WatchPermission(CefRefPtr<CefBrowser> b, CefString origin,
                              CefString id) {
    auto body = CefDictionaryValue::Create();
    body->SetString("id", id);
    Rpc::Send("watch", body, [b, origin, id](Dictionary result) {
      if (!b->IsValid())
        return;
      if (result && !result->GetBool("revoked")) {
        WatchPermission(b, origin, id);
        return;
      }
      b->GetHost()->GetRequestContext()->SetContentSetting(
          origin, origin, CEF_CONTENT_SETTING_TYPE_NOTIFICATIONS,
          CEF_CONTENT_SETTING_VALUE_BLOCK);
    });
  }
  void Register(CefRefPtr<CefBrowser> b, int index) {
    documents_[b->GetIdentifier()] = {index, 0};
  }
  void Close(CefRefPtr<CefBrowser> b) { documents_.erase(b->GetIdentifier()); }
  bool OnBeforeBrowse(CefRefPtr<CefBrowser> b, CefRefPtr<CefFrame> frame,
                      CefRefPtr<CefRequest>, bool, bool) override {
    if (frame->IsMain())
      ++documents_[b->GetIdentifier()].epoch;
    return false;
  }
  void Ask(CefRefPtr<CefBrowser> b, const std::string &kind,
           const std::string &target, std::function<void(Dictionary)> done) {
    auto it = documents_.find(b->GetIdentifier());
    if (it == documents_.end()) {
      done(nullptr);
      return;
    }
    auto snapshot = it->second;
    auto body = CefDictionaryValue::Create();
    body->SetString("kind", kind);
    body->SetString("tab", snapshot.index ? "B" : "A");
    body->SetString("source", b->GetMainFrame()->GetURL());
    body->SetString("target", target);
    body->SetInt("browserId", b->GetIdentifier());
    body->SetString("nativeEpoch", std::to_string(snapshot.epoch));
    CefRefPtr<NativePolicy> self = this;
    Rpc::Send("request", body, [self, b, snapshot, done](Dictionary result) {
      auto current = self->documents_.find(b->GetIdentifier());
      if (!b->IsValid() || current == self->documents_.end() ||
          current->second.epoch != snapshot.epoch) {
        done(nullptr);
        return;
      }
      done(result);
    });
  }
  bool Popup(CefRefPtr<CefBrowser> b, const CefString &target, bool gesture) {
    if (gesture)
      Ask(b, "popup", target.ToString(), [](Dictionary) {});
    return true; // Never create an unmanaged CEF top-level browser.
  }
  bool CanDownload(CefRefPtr<CefBrowser> b, const CefString &url,
                   const CefString &method) override {
    if (method == "GET")
      Ask(b, "download", url.ToString(), [b, url](Dictionary result) {
        if (result && result->GetString("choice") == "allow")
          AuthorizedDownload::Start(b, url.ToString(), result);
      });
    return false; // Even a DevTools allowAndName policy cannot bypass this.
  }
  bool OnBeforeDownload(CefRefPtr<CefBrowser>, CefRefPtr<CefDownloadItem>,
                        const CefString &,
                        CefRefPtr<CefBeforeDownloadCallback>) override {
    return true; // Dropping the callback cancels unsolicited StartDownload too.
  }
  void OnDownloadUpdated(CefRefPtr<CefBrowser>, CefRefPtr<CefDownloadItem> item,
                         CefRefPtr<CefDownloadItemCallback> callback) override {
    if (item->IsInProgress())
      callback->Cancel();
  }
  bool OnRequestMediaAccessPermission(
      CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, const CefString &, uint32_t,
      CefRefPtr<CefMediaAccessCallback> callback) override {
    callback->Cancel();
    return true;
  }
  bool OnShowPermissionPrompt(
      CefRefPtr<CefBrowser> b, uint64_t, const CefString &origin,
      uint32_t permissions,
      CefRefPtr<CefPermissionPromptCallback> callback) override {
    if (permissions != CEF_PERMISSION_TYPE_NOTIFICATIONS) {
      callback->Continue(CEF_PERMISSION_RESULT_DENY);
      return true;
    }
    Ask(b, "notifications", origin.ToString(),
        [b, origin, callback](Dictionary result) {
          bool allowed = result && result->GetString("choice") == "allow";
          callback->Continue(allowed ? CEF_PERMISSION_RESULT_ACCEPT
                                     : CEF_PERMISSION_RESULT_DENY);
          if (!allowed)
            return;
          WatchPermission(b, origin, result->GetString("id"));
        });
    return true;
  }

private:
  IMPLEMENT_REFCOUNTING(NativePolicy);
};
} // namespace browser_lab
#define BROWSER_LAB_POLICY_CLIENT                                              \
  CefRefPtr<browser_lab::NativePolicy> policy_ =                               \
      new browser_lab::NativePolicy();                                         \
  CefRefPtr<CefRequestHandler> GetRequestHandler() override {                  \
    return policy_;                                                            \
  }                                                                            \
  CefRefPtr<CefPermissionHandler> GetPermissionHandler() override {            \
    return policy_;                                                            \
  }                                                                            \
  CefRefPtr<CefDownloadHandler> GetDownloadHandler() override {                \
    return policy_;                                                            \
  }                                                                            \
  bool OnBeforePopup(CefRefPtr<CefBrowser> b, CefRefPtr<CefFrame>, int,        \
                     const CefString &url, const CefString &,                  \
                     WindowOpenDisposition, bool gesture,                      \
                     const CefPopupFeatures &, CefWindowInfo &,                \
                     CefRefPtr<CefClient> &, CefBrowserSettings &,             \
                     CefRefPtr<CefDictionaryValue> &, bool *) override {       \
    return policy_->Popup(b, url, gesture);                                    \
  }
#endif
