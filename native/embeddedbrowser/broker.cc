// Owned CEF browser process. No TCP/debug listener; control is a per-launch,
// mutually authenticated current-user pipe. CEF remains the DevTools engine.
#include <array>
#include <atomic>
#include <map>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <functional>
#include <memory>
#include <vector>
#include <algorithm>
#include <charconv>
#include "include/cef_app.h"
#include "include/cef_browser.h"
#include "include/cef_command_line.h"
#include "include/cef_devtools_message_observer.h"
#include "include/cef_parser.h"
#ifdef _WIN32
#include "include/cef_sandbox_win.h"
#endif
#include "include/cef_version_info.h"
#include "include/cef_scheme.h"
#include "include/cef_resource_handler.h"
#include "include/cef_request_context.h"
#include "include/cef_frame_handler.h"
#include "include/cef_jsdialog_handler.h"
#include "include/cef_download_handler.h"
#include "include/base/cef_bind.h"
#include "include/base/cef_callback.h"
#include "include/wrapper/cef_closure_task.h"
#include "include/wrapper/cef_helpers.h"

#ifdef _WIN32
#include "platform_win.h"
#elif defined(__APPLE__)
#include "mac/platform_mac.h"
#else
#include "platform_linux.h"
#endif
using namespace dsh_platform;

namespace {
using Dict = CefRefPtr<CefDictionaryValue>;
std::mutex output_mutex;
constexpr uint32_t kLimit = 8 << 20;
std::string generation;
std::atomic_bool stopping{false};
std::thread reader;
bool control_blocked = false;
void Write(Dict value) {
  auto v = CefValue::Create(); v->SetDictionary(value);
  auto json = CefWriteJSON(v, JSON_WRITER_DEFAULT).ToString();
  if (json.empty() || json.size() > kLimit) return;
  uint32_t n = static_cast<uint32_t>(json.size());
  std::lock_guard<std::mutex> lock(output_mutex);
  Transfer(true, &n, 4); Transfer(true, json.data(), n);
}
void Send(const std::string& route, Dict payload) {
  auto m = CefDictionaryValue::Create();
  m->SetString("generation", generation); m->SetString("route", route);
  m->SetDictionary("payload", payload); Write(m);
}
void Result(const std::string& route, int id, CefRefPtr<CefValue> value,
            const std::string& error = {}) {
  auto m = CefDictionaryValue::Create(); m->SetInt("id", id);
  if (error.empty()) m->SetValue("result", value);
  else m->SetString("error", error);
  Send(route, m);
}
CefRefPtr<CefValue> Empty() {
  auto v = CefValue::Create(); v->SetDictionary(CefDictionaryValue::Create()); return v;
}
void Event(const std::string& route, const std::string& method, CefRefPtr<CefListValue> params) {
  auto m = CefDictionaryValue::Create(); m->SetString("method", method);
  m->SetList("params", params); Send(route, m);
}
#include "artifact_resource.h"
void FinishArtifact(const std::shared_ptr<ArtifactState>& state);
class Client;
std::map<std::string,CefRefPtr<Client>> closing_clients;
struct Tab {
  std::string route;
  CefRefPtr<CefBrowser> browser;
  CefRefPtr<CefRegistration> observer;
  std::shared_ptr<ArtifactState> artifact;
};
std::map<int, Tab> tabs;
int next_tab = 0;
class Client final : public CefClient, public CefLifeSpanHandler,
                     public CefDevToolsMessageObserver, public CefRequestHandler,public CefFrameHandler,public CefLoadHandler,public CefDisplayHandler,public CefJSDialogHandler,public CefDownloadHandler
#ifdef __APPLE__
                     , public CefRenderHandler
#endif
{
  std::string route_,owner_route_;
  std::shared_ptr<ArtifactState> artifact_;
  int create_id_, tab_;
  std::set<std::string> engine_frames_;
  bool browser_closed_=false,closing_=false;
  std::string policy_denied_;
  std::vector<std::function<void()>> revoke_waiters_,close_waiters_;
  bool close_notified_=false,view_visible_=true,managed_=false;
  std::string view_lease_,presentation_epoch_,guard_view_,guard_presentation_,page_title_;
  void FlushRevocation(){
 if(!pending_.empty()||(browser_closed_&&!engine_frames_.empty()))return;
 auto callbacks=std::move(revoke_waiters_);revoke_waiters_.clear();for(auto& cb:callbacks)cb();
 if(browser_closed_&&engine_frames_.empty()){
  if(!close_notified_){close_notified_=true;auto event=CefDictionaryValue::Create();event->SetString("kind","target.closed");event->SetString("generation",generation);event->SetString("owner",owner_route_);Write(event);}
  auto closed=std::move(close_waiters_);close_waiters_.clear();for(auto& cb:closed)cb();
 }
}
  uint64_t document_epoch_=1,scope_epoch_=1;
  bool loaded_=false,guard_required_=false,guard_active_=false,navigating_=false;
  std::string document_url_,guard_document_,guard_url_,guard_token_,navigation_origin_,guard_scope_;
  static std::string Origin(const std::string& url){CefURLParts parts;if(!CefParseURL(url,parts))return {};auto origin=CefString(&parts.origin).ToString();if(!origin.empty()&&origin.back()=='/')origin.pop_back();return origin;}
  void DocumentChanged(const std::string& url){
    document_epoch_++;document_url_=url;

  }
  std::map<int, int> pending_;
  std::map<int, std::string> pending_methods_;
  int next_command_ = 0;
  std::set<std::string> child_sessions_;
  std::set<std::string> frames_;
  void RememberFrames(Dict tree) {
    if (!tree) return;
    auto frame = tree->GetDictionary("frame");
    if (frame) frames_.insert(frame->GetString("id").ToString());
    auto children = tree->GetList("childFrames");
    if (children) for (size_t i=0;i<children->GetSize();++i) RememberFrames(children->GetDictionary(i));
  }
public:
  Client(std::string route, int id,std::shared_ptr<ArtifactState> artifact={},bool managed=false) : route_(route),owner_route_(std::move(route)),artifact_(std::move(artifact)), create_id_(id), tab_(++next_tab) {managed_=managed;guard_required_=managed;view_visible_=!managed;}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefRequestHandler> GetRequestHandler() override { return this; }
  CefRefPtr<CefLoadHandler> GetLoadHandler()override{return this;}
  CefRefPtr<CefDisplayHandler> GetDisplayHandler()override{return this;}
  CefRefPtr<CefJSDialogHandler> GetJSDialogHandler()override{return this;}
  CefRefPtr<CefDownloadHandler> GetDownloadHandler()override{return this;}
#ifdef __APPLE__
  CefRefPtr<CefRenderHandler> GetRenderHandler() override { return this; }
  void GetViewRect(CefRefPtr<CefBrowser>, CefRect& rect) override {
    dsh_platform::ViewRect(rect);
  }
  void OnPaint(CefRefPtr<CefBrowser>, CefRenderHandler::PaintElementType,
               const CefRenderHandler::RectList&, const void* buffer,
               int width, int height) override {
    dsh_platform::Paint(buffer, width, height);
  }
#endif
  void RejectUnsupportedPolicy(const std::string& error){
    // Authority belongs only to the installed call. A manual page event emits
    // diagnostics, but cannot poison a future unrelated permit.
    if(guard_active_&&policy_denied_.empty())policy_denied_=error;
    auto event=CefDictionaryValue::Create();event->SetString("kind","policy.denied");
    event->SetString("generation",generation);event->SetString("owner",owner_route_);
    event->SetString("method",error);Write(event);
    for(const auto& [_,id]:pending_)Result(route_,id,nullptr,error);
    pending_.clear();pending_methods_.clear();FlushRevocation();
  }
  bool OnJSDialog(CefRefPtr<CefBrowser>,const CefString&,JSDialogType,const CefString&,const CefString&,CefRefPtr<CefJSDialogCallback>,bool& suppress)override{
    suppress=true;RejectUnsupportedPolicy("CEF_DIALOG_UNSUPPORTED");return false;
  }
  bool OnBeforeUnloadDialog(CefRefPtr<CefBrowser>,const CefString&,bool,CefRefPtr<CefJSDialogCallback> callback)override{
    if(closing_){callback->Continue(true,{});return true;}
    RejectUnsupportedPolicy("CEF_DIALOG_UNSUPPORTED");callback->Continue(false,{});return true;
  }
  bool CanDownload(CefRefPtr<CefBrowser>,const CefString&,const CefString&)override{
    RejectUnsupportedPolicy("CEF_DOWNLOAD_UNSUPPORTED");return false;
  }
  bool OnBeforeDownload(CefRefPtr<CefBrowser>,CefRefPtr<CefDownloadItem>,const CefString&,CefRefPtr<CefBeforeDownloadCallback>)override{
    // No Continue: destruction of the official callback cancels either style,
    // including any engine path that did not go through CanDownload.
    RejectUnsupportedPolicy("CEF_DOWNLOAD_UNSUPPORTED");return true;
  }
  const std::string& PolicyDenial()const{return policy_denied_;}
  void OnLoadStart(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,TransitionType)override{if(frame->IsMain()){loaded_=false;DocumentChanged(frame->GetURL());}else scope_epoch_++;}
  void OnLoadEnd(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,int)override{if(frame->IsMain()){loaded_=true;}}
  void OnTitleChange(CefRefPtr<CefBrowser>,const CefString& title)override{page_title_=title;}
  void OnAddressChange(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,const CefString& url)override{if(frame->IsMain()){if(url.ToString()!=document_url_)DocumentChanged(url);}else scope_epoch_++;}
  bool ViewVisible(){if(closing_||!view_visible_)return false;if(view_lease_.empty())return !managed_;if(!view_visible_||!tabs.contains(tab_))return false;auto hwnd=tabs.at(tab_).browser->GetHost()->GetWindowHandle();return dsh_platform::Visible(hwnd);}
  Dict NavigationState(){if(closing_||!tabs.contains(tab_))return nullptr;auto browser=tabs.at(tab_).browser;auto value=CefDictionaryValue::Create();value->SetString("url",browser->GetMainFrame()->GetURL());value->SetString("title",page_title_.substr(0,4096));value->SetBool("loading",browser->IsLoading());value->SetBool("canGoBack",browser->CanGoBack());value->SetBool("canGoForward",browser->CanGoForward());return value;}
  bool NavigationAction(const std::string& action){if(control_blocked||closing_||!tabs.contains(tab_))return false;auto browser=tabs.at(tab_).browser;RevokeGuard();if(action=="reload")browser->Reload();else if(action=="back"&&browser->CanGoBack())browser->GoBack();else if(action=="forward"&&browser->CanGoForward())browser->GoForward();else return false;return true;}
  bool FrameScopeReady(){
    if(!tabs.contains(tab_))return false;
    auto browser=tabs.at(tab_).browser;
    std::vector<CefString> ids;browser->GetFrameIdentifiers(ids);
    for(const auto& id:ids){auto frame=browser->GetFrameByIdentifier(id);if(frame&&frame->IsValid()&&!frame->IsMain())return false;}
    return true;
  }
  Dict OwnedState(){
    if(closing_||!loaded_||!tabs.contains(tab_)||(artifact_&&artifact_->revoked))return nullptr;
    auto frame=tabs.at(tab_).browser->GetMainFrame();if(!frame||!frame->IsValid())return nullptr;
    auto state=CefDictionaryValue::Create();state->SetString("brokerGeneration",generation);state->SetString("target",std::to_string(tab_));
    state->SetString("frame",frame->GetIdentifier());state->SetString("document",std::to_string(document_epoch_));state->SetString("url",frame->GetURL());
    state->SetBool("viewVisible",ViewVisible());if(!view_lease_.empty()){state->SetString("viewLease",view_lease_);state->SetString("presentationEpoch",presentation_epoch_);}
    state->SetBool("frameScopeReady",FrameScopeReady());state->SetString("scopeEpoch",std::to_string(scope_epoch_));
    if(artifact_){state->SetString("artifactLease",artifact_->lease);state->SetString("artifactOrigin",artifact_->origin);}return state;
  }
  std::string StateStatus(){if(closing_)return "CEF_TARGET_CLOSING";return "CEF_TARGET_NOT_READY loaded="+std::to_string(loaded_)+" document="+std::to_string(document_epoch_)+" loading="+std::to_string(tabs.contains(tab_)?tabs.at(tab_).browser->IsLoading():false);}
  bool BeginGuard(Dict call){
 if(control_blocked||closing_)return false;
    guard_required_=true;
    auto actual=OwnedState(),expected=call?call->GetDictionary("identity"):nullptr;
    if(!actual||!ViewVisible()||!actual->GetBool("frameScopeReady")||!expected||!actual->IsEqual(expected)||call->GetString("token").empty())return false;
    guard_document_=actual->GetString("document");guard_url_=actual->GetString("url");guard_token_=call->GetString("token");
    navigation_origin_=call->GetString("navigationOrigin");guard_scope_=actual->GetString("scopeEpoch");guard_view_=view_lease_;guard_presentation_=presentation_epoch_;navigating_=false;guard_active_=true;
    return true;
  }
  bool EndGuard(const std::string& token){if(guard_active_&&token!=guard_token_)return false;RevokeGuard();return true;}
  void RevokeGuard(){guard_active_=false;navigating_=false;policy_denied_.clear();}
  void PauseControl(){
    RevokeGuard();
    // Retire result ownership immediately. A renderer promise need not finish
    // before control cleanup; late replies to these serials are discarded.
    for(const auto& [_,id]:pending_)Result(route_,id,nullptr,"CEF_BROKER_CONTROL_PAUSED");
    pending_.clear();pending_methods_.clear();FlushRevocation();
  }
  void RevokeWhenDrained(std::function<void()> done){RevokeGuard();revoke_waiters_.push_back(std::move(done));FlushRevocation();}
  bool HasPending()const{return !pending_.empty();}  void CloseWhenDrained(std::function<void()> done){closing_=true;RevokeGuard();view_visible_=false;close_waiters_.push_back(std::move(done));if(tabs.contains(tab_))tabs.at(tab_).browser->GetHost()->CloseBrowser(true);FlushRevocation();}
  std::string Present(Dict args){
 if(control_blocked)return "CEF_BROKER_CONTROL_PAUSED";
 if(closing_)return "CEF_TARGET_CLOSING";
    managed_=true;guard_required_=true;RevokeGuard();view_visible_=false;
    if(!tabs.contains(tab_))return "CEF_PRESENT_TARGET_GONE";
    auto browser=tabs.at(tab_).browser;auto hwnd=browser->GetHost()->GetWindowHandle();
    auto error=dsh_platform::Present(hwnd,args);if(!error.empty())return error;
    auto bounds=args->GetDictionary("bounds");bool visible=args->GetBool("visible")&&bounds->GetInt("Width")>0&&bounds->GetInt("Height")>0;
    // No identity changes until every operation and final OS readback passed.
    view_lease_=args->GetString("view");presentation_epoch_=args->GetString("epoch");view_visible_=visible;
    browser->GetHost()->NotifyMoveOrResizeStarted();browser->GetHost()->WasResized();return {};
  }
  std::string Hide(Dict args){managed_=true;guard_required_=true;RevokeGuard();view_visible_=false;if(args)presentation_epoch_=args->GetString("epoch");if(!tabs.contains(tab_))return "CEF_PRESENT_TARGET_GONE";return dsh_platform::Hide(tabs.at(tab_).browser->GetHost()->GetWindowHandle());}
  bool Navigate(const std::string& url){auto origin=Origin(url);if(control_blocked||closing_||!tabs.contains(tab_)||(origin.rfind("http://",0)!=0&&origin.rfind("https://",0)!=0))return false;if(artifact_&&!ArtifactURL(artifact_,url))return false;RevokeGuard();tabs.at(tab_).browser->GetMainFrame()->LoadURL(url);return true;}
  bool GuardValid(){
 if(control_blocked||closing_)return false;
    if(!guard_required_)return true;
    if(!guard_active_||!ViewVisible()||view_lease_!=guard_view_||presentation_epoch_!=guard_presentation_||navigating_||!tabs.contains(tab_)||!FrameScopeReady()||guard_scope_!=std::to_string(scope_epoch_))return false;
    return guard_document_==std::to_string(document_epoch_)&&guard_url_==tabs.at(tab_).browser->GetMainFrame()->GetURL().ToString();
  }
  // Old-document authority never migrates to a navigation result. Only target-
// scoped lifecycle subscriptions needed to complete Page.goto may continue;
// Input and Runtime.evaluate/callFunctionOn are deliberately absent.
  bool NavigationWaitMethod(const std::string& method){
    if(control_blocked||closing_||!guard_required_||!guard_active_||!ViewVisible()||view_lease_!=guard_view_||presentation_epoch_!=guard_presentation_||!navigating_||navigation_origin_.empty()||!tabs.contains(tab_)||!FrameScopeReady()||guard_scope_!=std::to_string(scope_epoch_))return false;
    auto current_origin=Origin(tabs.at(tab_).browser->GetMainFrame()->GetURL());
    if(current_origin!=navigation_origin_&&current_origin!=Origin(guard_url_))return false;
    static const std::set<std::string> lifecycle={"Page.enable","Page.getFrameTree","Page.setLifecycleEventsEnabled","Page.createIsolatedWorld","Runtime.enable","Runtime.runIfWaitingForDebugger","Network.enable","Log.enable","Target.setAutoAttach","Runtime.releaseObject"};
    return lifecycle.contains(method);
  }
bool RequiresGuard()const{return guard_required_;}
  bool OwnsRoute(const std::string& route)const{return owner_route_==route;}
  bool AttachRoute(const std::string& route){
    if(control_blocked||closing_||!tabs.contains(tab_)||!tabs.at(tab_).route.empty()||!pending_.empty()||guard_active_)return false;
    route_=route;auto& tab=tabs.at(tab_);tab.route=route;frames_.clear();child_sessions_.clear();
    // CEF retains its per-browser DevTools agent across observer registrations.
    // Reset only this target domain subscription so the replacement SDK gets
    // fresh executionContextCreated/frame events rather than stale enabled state.
    for(const auto& method:{"Runtime.disable","Page.disable","Network.disable","Log.disable"}){
      auto reset=CefDictionaryValue::Create();reset->SetInt("id",++next_command_);reset->SetString("method",method);auto value=CefValue::Create();value->SetDictionary(reset);auto json=CefWriteJSON(value,JSON_WRITER_DEFAULT).ToString();if(!tab.browser->GetHost()->SendDevToolsMessage(json.data(),json.size()))return false;
    }
    if(!tab.observer)tab.observer=tab.browser->GetHost()->AddDevToolsMessageObserver(this);
    if(!tab.observer)return false;
    auto args=CefListValue::Create();auto info=CefDictionaryValue::Create();info->SetInt("id",tab_);info->SetString("url",tab.browser->GetMainFrame()->GetURL());args->SetDictionary(0,info);Event(route_,"tab.created",args);Event(route_,"route.ready",CefListValue::Create());return true;
  }
  CefRefPtr<CefFrameHandler> GetFrameHandler()override{return this;}
  void OnFrameCreated(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame)override {
    engine_frames_.insert(frame->GetIdentifier().ToString());if(!frame->IsMain())scope_epoch_++;
    if(artifact_)artifact_->live_frames.insert(frame->GetIdentifier().ToString());
  }
  void OnFrameDestroyed(CefRefPtr<CefBrowser> browser,CefRefPtr<CefFrame> frame)override {
#if defined(__APPLE__)
    dsh_platform::Lifecycle("frame-destroyed-enter");
#endif
    engine_frames_.erase(frame->GetIdentifier().ToString());if(!frame->IsMain())scope_epoch_++;
    if(artifact_){artifact_->live_frames.erase(frame->GetIdentifier().ToString());artifact_->destroyed_frames++;
      if(artifact_->browser>0)ArtifactEvent(artifact_,"artifact.detached",browser->GetIdentifier(),frame->GetIdentifier().ToString());
      FinishArtifact(artifact_);
    }
    FlushRevocation();
    if(browser_closed_&&engine_frames_.empty()){closing_clients.erase(owner_route_);if(stopping&&tabs.empty()&&artifacts.empty()&&closing_clients.empty())CefQuitMessageLoop();}
#if defined(__APPLE__)
    dsh_platform::Lifecycle("frame-destroyed-return");
#endif
  }
  void OnAfterCreated(CefRefPtr<CefBrowser> b) override {
    tabs.emplace(tab_, Tab{route_, b, b->GetHost()->AddDevToolsMessageObserver(this),artifact_});
    if(artifact_){artifact_->browser=b->GetIdentifier();ArtifactEvent(artifact_,"artifact.bound",b->GetIdentifier());artifact_->admitted=true;CefPostTask(TID_IO,base::BindOnce(AdmitArtifactResources,artifact_));}
    auto tab = CefDictionaryValue::Create(); tab->SetInt("id", tab_);
    tab->SetString("url", b->GetMainFrame()->GetURL());
    auto args = CefListValue::Create(); args->SetDictionary(0, tab->Copy(false));
    Event(route_, "tab.created", args);
    if (create_id_) { auto v = CefValue::Create(); v->SetDictionary(tab); Result(route_, create_id_, v); }
    else Event(route_, "route.ready", CefListValue::Create());
  }
  bool DoClose(CefRefPtr<CefBrowser> browser) override {
    closing_=true;RevokeGuard();view_visible_=false;
    if(!managed_)return false;
#ifndef _WIN32
    // Locked CEF's Linux CloseHostWindow addresses its own CefWindowX11, not
    // the external parent. Keep that maintained lifecycle instead of copying
    // the Windows ancestor-close workaround.
    return false;
#else
    // CEF's default Alloy CloseHostWindow posts WM_CLOSE to GA_ROOT, which
    // is the external UI after presentation. Tear down only our own guest
    // asynchronously; final ACK still requires OnBeforeClose + all frames.
    const auto guest=browser->GetHost()->GetWindowHandle();
    CefPostTask(TID_UI,base::BindOnce([](CefRefPtr<CefBrowser> current,CefWindowHandle expected){
      if(!current->IsValid()||current->GetHost()->GetWindowHandle()!=expected)return;
      dsh_platform::DestroyOwnedGuest(expected);
    },browser,guest));
    return true;
#endif
  }
  void OnBeforeClose(CefRefPtr<CefBrowser>) override {
#if defined(__APPLE__)
    dsh_platform::Lifecycle("before-close");
#endif
    browser_closed_=true;closing_clients[owner_route_]=this;pending_.clear();pending_methods_.clear();
    tabs.erase(tab_);
#if defined(__APPLE__)
    dsh_platform::Lifecycle("before-close-tab-erased");
#endif
    if(artifact_){artifact_->closed_browsers++;FinishArtifact(artifact_);}
    auto args = CefListValue::Create(); args->SetInt(0, tab_); Event(route_, "tab.closed", args);
    FlushRevocation();
    if (stopping && tabs.empty()&&artifacts.empty()&&closing_clients.empty()) CefQuitMessageLoop();
#if defined(__APPLE__)
    dsh_platform::Lifecycle("before-close-return");
#endif
  }
  bool OnBeforePopup(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame>, int,
      const CefString&, const CefString&, CefLifeSpanHandler::WindowOpenDisposition, bool,
      const CefPopupFeatures&, CefWindowInfo&, CefRefPtr<CefClient>&,
      CefBrowserSettings&, CefRefPtr<CefDictionaryValue>&, bool*) override { return true; }
  bool OnBeforeBrowse(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,CefRefPtr<CefRequest> request,bool,bool)override {
    if(artifact_&&(artifact_->revoked||!ArtifactURL(artifact_,request->GetURL())))return true;
    if(frame->IsMain()&&guard_required_&&guard_active_){auto origin=Origin(request->GetURL());if(origin.empty()||(!navigation_origin_.empty()&&origin!=navigation_origin_))return true;}
    return false;
  }
  void OnRenderProcessTerminated(CefRefPtr<CefBrowser> browser, TerminationStatus status, int error_code,
      const CefString& error_string) override {
    // This reports a browser callback, not the complete renderer influence set.
    // Pause before publishing the event, and retire all installed authority.
    control_blocked = true;
    for(const auto& [_,tab]:tabs)static_cast<Client*>(tab.browser->GetHost()->GetClient().get())->PauseControl();
    auto m = CefDictionaryValue::Create(); m->SetString("kind", "fault");
    m->SetString("generation", generation); m->SetString("scope", "broker");
    m->SetInt("reportedBrowser",browser->GetIdentifier());m->SetInt("reportedTab",tab_);
    m->SetInt("terminationStatus",static_cast<int>(status));m->SetInt("errorCode",error_code);
    // Arbitrary engine strings may contain page data. Keep typed codes only.
    m->SetBool("errorTextOmitted",!error_string.empty());
    auto frame=browser->GetMainFrame();if(frame)m->SetString("reportedMainFrame",frame->GetIdentifier());
    Write(m);
  }
  void Dispatch(int id, const std::string& method, Dict params, const std::string& session) {
 if(control_blocked){Result(route_,id,nullptr,"CEF_BROKER_CONTROL_PAUSED");return;}
 if(closing_){Result(route_,id,nullptr,"CEF_TARGET_CLOSING");return;}
    if(!policy_denied_.empty()){Result(route_,id,nullptr,policy_denied_);return;}
    if(artifact_&&artifact_->revoked){Result(route_,id,nullptr,"CEF_ARTIFACT_REVOKED");return;}
    if(!GuardValid()&&!NavigationWaitMethod(method)){Result(route_,id,nullptr,"CEF_DOCUMENT_AUTHORIZATION_CHANGED");return;}
    if(guard_required_&&method=="Page.navigate"){
      if(!params||navigation_origin_.empty()||Origin(params->GetString("url"))!=navigation_origin_){Result(route_,id,nullptr,"CEF_ORIGIN_AUTHORIZATION_REQUIRED");return;}
      navigating_=true;
    }
    if (!session.empty() && !child_sessions_.contains(session)) {
      Result(route_, id, nullptr, "CEF_CROSS_SESSION_REJECTED"); return;
    }
    if (params && params->HasKey("frameId") && !frames_.contains(params->GetString("frameId").ToString())) {
      Result(route_, id, nullptr, "CEF_CROSS_FRAME_REJECTED"); return;
    }
    auto m = CefDictionaryValue::Create(); int serial = ++next_command_;
    m->SetInt("id", serial); m->SetString("method", method);
    if (params) m->SetDictionary("params", params);
    if (!session.empty()) m->SetString("sessionId", session);
    auto v = CefValue::Create(); v->SetDictionary(m);
    auto json = CefWriteJSON(v, JSON_WRITER_DEFAULT).ToString();
    pending_[serial] = id;
    pending_methods_[serial] = method;
    if (!tabs.at(tab_).browser->GetHost()->SendDevToolsMessage(json.data(), json.size())) {
      pending_.erase(serial); pending_methods_.erase(serial); Result(route_, id, nullptr, "CEF_DEVTOOLS_DISPATCH_REJECTED");
    }else{
      auto observed=CefDictionaryValue::Create();observed->SetString("kind","dispatch.observed");observed->SetString("generation",generation);observed->SetString("route",route_);observed->SetString("method",method);Write(observed);
    }
  }
  bool OnDevToolsMessage(CefRefPtr<CefBrowser>, const void* data, size_t size) override {
    auto value = CefParseJSON(CefString(static_cast<const char*>(data), size), JSON_PARSER_RFC);
    auto m = value ? value->GetDictionary() : nullptr;
    if (!m) return true;
    if (m->HasKey("id")) {
      auto p = pending_.find(m->GetInt("id")); if (p == pending_.end()) return true;
      auto id = p->second; pending_.erase(p);
      auto method = pending_methods_.extract(m->GetInt("id"));
      if (!method.empty() && method.mapped() == "Page.getFrameTree" && m->GetDictionary("result"))
        RememberFrames(m->GetDictionary("result")->GetDictionary("frameTree"));
      if (control_blocked) Result(route_, id, nullptr, "CEF_BROKER_CONTROL_PAUSED");
      else if (m->HasKey("error")) Result(route_, id, nullptr, m->GetDictionary("error")->GetString("message").ToString());
      else Result(route_, id, m->GetValue("result"));
      FlushRevocation();
    } else {
      const auto method = m->GetString("method").ToString();
      auto params = m->GetDictionary("params");
      if (params && method == "Target.attachedToTarget") child_sessions_.insert(params->GetString("sessionId").ToString());
      if (params && method == "Target.detachedFromTarget") child_sessions_.erase(params->GetString("sessionId").ToString());
      if (params && method == "Page.frameNavigated" && params->GetDictionary("frame")) frames_.insert(params->GetDictionary("frame")->GetString("id").ToString());
      if (params && method == "Page.frameDetached") frames_.erase(params->GetString("frameId").ToString());
      if(control_blocked)return true;
      auto args = CefListValue::Create(); auto source = CefDictionaryValue::Create();
      source->SetInt("tabId", tab_);
      if (m->HasKey("sessionId")) source->SetString("sessionId", m->GetString("sessionId"));
      args->SetDictionary(0, source); args->SetString(1, m->GetString("method"));
      args->SetDictionary(2, m->GetDictionary("params")); Event(route_, "devtools.event", args);
    }
    return true;
  }
  IMPLEMENT_REFCOUNTING(Client);
};
bool Create(const std::string& route, int id, const std::string& url,std::shared_ptr<ArtifactState> artifact={},bool managed=false) {
  CefWindowInfo window;
  if(!dsh_platform::ConfigureWindow(window)){Result(route,id,nullptr,"CEF_NATIVE_WINDOW_UNAVAILABLE");return false;}
  CefBrowserSettings settings;
  if (!CefBrowserHost::CreateBrowserSync(window, new Client(route, id,artifact,managed), url, settings, nullptr,artifact?artifact->context:nullptr)) {
    Result(route, id, nullptr, "CEF_CREATE_FAILED");
    return false;
  }
  return true;
}
void FailArtifactOpen(const std::shared_ptr<ArtifactState>& state) {
  state->revoked=true;
  CefPostTask(TID_IO,base::BindOnce(DrainArtifactResources,state,base::BindOnce([](std::shared_ptr<ArtifactState> state){
    {std::lock_guard<std::mutex> lock(state->context_mutex);if(state->context){state->context->ClearSchemeHandlerFactories();state->context=nullptr;}}
    auto event=CefDictionaryValue::Create();event->SetString("kind","artifact.failed");event->SetString("generation",generation);
    event->SetString("lease",state->lease);event->SetString("partition",state->partition);Write(event);
    artifacts.erase(state->lease);
  },state)));
}
void FinishArtifact(const std::shared_ptr<ArtifactState>& state) {
  if(!artifacts.contains(state->lease)||!state->draining||!state->resources_drained)return;
  if(!state->live_frames.empty())return;
  for(const auto& [_,tab]:tabs)if(tab.artifact==state)return;
  bool cleared=false;
  {std::lock_guard<std::mutex> lock(state->context_mutex);cleared=state->context->ClearSchemeHandlerFactories();state->context=nullptr;}
  auto event=CefDictionaryValue::Create();event->SetString("kind","artifact.revoked");event->SetString("generation",generation);
  event->SetString("lease",state->lease);event->SetString("partition",state->partition);
  event->SetInt("closedBrowsers",state->closed_browsers);event->SetInt("canceledStreams",state->canceled_streams);
  event->SetInt("closedFrames",state->destroyed_frames);event->SetInt("liveFrames",static_cast<int>(state->live_frames.size()));
  event->SetInt("revokedFrames",state->revocation_frames);
  event->SetInt("readMarshaled",state->read_marshaled.load());
  event->SetBool("partitionRetired",cleared);if(!cleared)event->SetString("error","CEF_ARTIFACT_FACTORY_CLEAR_FAILED");Write(event);artifacts.erase(state->lease);
  if(stopping&&tabs.empty()&&artifacts.empty()&&closing_clients.empty())CefQuitMessageLoop();
}
void RevokeArtifact(std::shared_ptr<ArtifactState> state) {
  if(state->draining)return;
  state->draining=true;state->revoked=true;
  state->revocation_frames=static_cast<int>(state->live_frames.size());
  CefPostTask(TID_IO,base::BindOnce(DrainArtifactResources,state,base::BindOnce([](std::shared_ptr<ArtifactState> state){
    state->resources_drained=true;
    std::vector<CefRefPtr<CefBrowser>> browsers;
    for(const auto& [_,tab]:tabs)if(tab.artifact==state)browsers.push_back(tab.browser);
    for(auto& browser:browsers){browser->StopLoad();browser->GetHost()->CloseBrowser(true);}
    FinishArtifact(state);
  },state)));
}
void Shutdown() {
#if defined(__APPLE__)
  dsh_platform::Lifecycle("shutdown-enter");
#endif
  if (stopping.exchange(true)) return;
  std::vector<std::shared_ptr<ArtifactState>> states;
  for(const auto& [_,state]:artifacts)states.push_back(state);
  for(auto& state:states)RevokeArtifact(state);
  // Windowless CloseBrowser may synchronously call OnBeforeClose and erase
  // tabs. Own the selected browser references before invoking any callback.
  std::vector<CefRefPtr<CefBrowser>> browsers;
  for (const auto& [_, tab] : tabs) if(!tab.artifact)browsers.push_back(tab.browser);
  for (auto& browser : browsers) browser->GetHost()->CloseBrowser(true);
  if(tabs.empty()&&artifacts.empty()&&closing_clients.empty())CefQuitMessageLoop();
}
// This candidate admits only the real locked-MCP inventory exercised by the
// transport gate. New protocol methods require scope review and live evidence.
// CEF validates protocol types; this boundary additionally rejects unknown
// fields and methods before they can select browser/profile-wide state.
bool Admitted(const std::string& method, Dict params) {
  static const std::map<std::string, std::set<std::string>> allowed = {
    {"DOM.describeNode", {"nodeId","backendNodeId","objectId","depth","pierce"}},
    {"DOM.getContentQuads", {"nodeId","backendNodeId","objectId"}},
    {"DOM.resolveNode", {"nodeId","backendNodeId","objectGroup","executionContextId"}},
    {"DOM.scrollIntoViewIfNeeded", {"nodeId","backendNodeId","objectId","rect"}},
    {"Input.dispatchMouseEvent", {"type","x","y","modifiers","timestamp","button","buttons","clickCount","force","tangentialPressure","tiltX","tiltY","twist","deltaX","deltaY","pointerType"}},
    {"Input.insertText", {"text"}},
    {"Log.enable", {}},
    {"Network.enable", {"maxTotalBufferSize","maxResourceBufferSize","maxPostDataSize","reportDirectSocketTraffic","enableDurableMessages"}},
    {"Page.addScriptToEvaluateOnNewDocument", {"source","worldName","includeCommandLineAPI","runImmediately"}},
    {"Page.bringToFront", {}},
    {"Page.captureScreenshot", {"format","quality","clip","fromSurface","captureBeyondViewport","optimizeForSpeed"}},
    {"Page.createIsolatedWorld", {"frameId","worldName","grantUniveralAccess"}},
    {"Page.enable", {"enableFileChooserOpenedEvent"}},
    {"Page.getFrameTree", {}},
    {"Page.getLayoutMetrics", {}},
    {"Page.navigate", {"url","referrer","transitionType","frameId","referrerPolicy"}},
    {"Page.setInterceptFileChooserDialog", {"enabled","cancel"}},
    {"Page.setLifecycleEventsEnabled", {"enabled"}},
    {"Runtime.callFunctionOn", {"functionDeclaration","objectId","arguments","silent","returnByValue","generatePreview","userGesture","awaitPromise","executionContextId","objectGroup","throwOnSideEffect","uniqueContextId","serializationOptions"}},
    {"Runtime.enable", {}},
    {"Runtime.evaluate", {"expression","objectGroup","includeCommandLineAPI","silent","contextId","returnByValue","generatePreview","userGesture","awaitPromise","throwOnSideEffect","timeout","disableBreaks","replMode","allowUnsafeEvalBlockedByCSP","uniqueContextId","serializationOptions"}},
    {"Runtime.releaseObject", {"objectId"}},
    {"Runtime.runIfWaitingForDebugger", {}},
    {"Target.getTargetInfo", {}},
    {"Target.setAutoAttach", {"autoAttach","waitForDebuggerOnStart","flatten"}},
  };
  auto rule = allowed.find(method);
  if (rule == allowed.end()) return false;
  if (params) {
    CefDictionaryValue::KeyList keys;
    params->GetKeys(keys);
    for (const auto& key : keys) if (!rule->second.contains(key.ToString())) return false;
    if (method == "Target.setAutoAttach" &&
        (!params->GetBool("flatten") || params->GetType("autoAttach")!=VTYPE_BOOL || params->GetType("waitForDebuggerOnStart")!=VTYPE_BOOL)) return false;
    // Locked Playwright requests its private utility world with this flag.
    // Dispatch additionally binds frameId to this browser's observed frame set.
    if (method == "Page.createIsolatedWorld" && params->GetString("worldName").ToString().rfind("__playwright_utility_world_",0)!=0) return false;
  }
  return true;
}
void Command(Dict m) {
  if (stopping) return;
  if (m->GetString("generation").ToString() != generation) return;
  auto route = m->GetString("route").ToString();
  auto kind = m->GetString("kind").ToString();
  if(kind.rfind("target.",0)==0){
    auto response=CefDictionaryValue::Create();response->SetString("kind","target.result");response->SetString("generation",generation);response->SetString("request",m->GetString("request"));
    Client* client=nullptr;for(const auto& [_,tab]:tabs){auto candidate=static_cast<Client*>(tab.browser->GetHost()->GetClient().get());if(candidate->OwnsRoute(route)){client=candidate;break;}}
    if(!client&&closing_clients.contains(route))client=closing_clients.at(route).get();
    if(!client){if(kind=="target.revoke"||kind=="target.end")response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error","CEF_TARGET_NOT_READY");}
    else if(control_blocked&&kind!="target.state"&&kind!="target.navigation"&&kind!="target.hide"&&kind!="target.end"&&kind!="target.revoke"&&kind!="target.close")response->SetString("error","CEF_BROKER_CONTROL_PAUSED");
    else if(kind=="target.state"){auto state=client->OwnedState();if(state)response->SetDictionary("result",state);else response->SetString("error",client->StateStatus());}
    else if(kind=="target.attach"){if(client->AttachRoute(m->GetDictionary("arguments")->GetString("route")))response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error","CEF_OBSERVER_REATTACH_REJECTED");}
    else if(kind=="target.navigation"){auto state=client->NavigationState();if(state)response->SetDictionary("result",state);else response->SetString("error","CEF_TARGET_NOT_READY");}
    else if(kind=="target.action"){if(client->NavigationAction(m->GetDictionary("arguments")->GetString("action")))response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error","CEF_NAVIGATION_ACTION_UNAVAILABLE");}
    else if(kind=="target.present"){auto error=client->Present(m->GetDictionary("arguments"));if(error.empty())response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error",error);}
    else if(kind=="target.hide"){auto error=client->Hide(m->GetDictionary("arguments"));if(error.empty())response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error",error);}
    else if(kind=="target.navigate"){if(client->Navigate(m->GetDictionary("arguments")->GetString("url")))response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error","CEF_NAVIGATION_REJECTED");}
    else if(kind=="target.close"){response->SetDictionary("result",CefDictionaryValue::Create());client->CloseWhenDrained([response]{Write(response);});return;}
    else if(kind=="target.begin"){if(client->BeginGuard(m->GetDictionary("arguments")))response->SetDictionary("result",CefDictionaryValue::Create());else response->SetString("error","CEF_DOCUMENT_AUTHORIZATION_CHANGED");}
    else if(kind=="target.end"){auto denied=client->PolicyDenial();if(client->EndGuard(m->GetDictionary("arguments")->GetString("token"))){if(!denied.empty())response->SetString("error",denied);else response->SetDictionary("result",CefDictionaryValue::Create());}else response->SetString("error","CEF_CALL_TOKEN_REJECTED");}
    else if(kind=="target.revoke"){response->SetDictionary("result",CefDictionaryValue::Create());client->RevokeWhenDrained([response]{Write(response);});return;}
    else response->SetString("error","CEF_TARGET_METHOD_REJECTED");
    Write(response);return;
  }
  if(kind=="resource.result"){CefPostTask(TID_IO,base::BindOnce(ArtifactResponse,m));return;}
  if(kind=="artifact.revoke"){
    auto found=artifacts.find(m->GetString("lease").ToString());
    if(found!=artifacts.end()&&found->second->partition==m->GetString("partition").ToString())RevokeArtifact(found->second);
    return;
  }
  if (kind == "shutdown") { Shutdown(); return; }
  // Lifecycle disposal remains available while control is conservatively
  // paused. Releasing an Agent route never destroys broker-owned documents.
  if (kind == "release") {
    for (auto& [_, t] : tabs) if (t.route == route) {auto client=static_cast<Client*>(t.browser->GetHost()->GetClient().get());client->RevokeGuard();t.route.clear();if(!client->HasPending())t.observer = nullptr; }
    return;
  }
  if (kind == "close") {
    std::vector<CefRefPtr<CefBrowser>> browsers;
    for (const auto& [_, t] : tabs) if (t.route == route) browsers.push_back(t.browser);
    for (auto& browser : browsers) browser->GetHost()->CloseBrowser(true);
    return;
  }
  if (control_blocked) {auto payload=m->GetDictionary("payload");if(payload)Result(route,payload->GetInt("id"),nullptr,"CEF_BROKER_CONTROL_PAUSED");return;}
  if(kind=="artifact.open"){
    auto state=std::make_shared<ArtifactState>();state->lease=m->GetString("lease");state->partition=m->GetString("partition");state->origin=m->GetString("origin");state->route=route;
    if(artifacts.contains(state->lease))return;
    CefRequestContextSettings settings;
    state->context=CefRequestContext::CreateContext(settings,nullptr);
    if(!state->context||!state->context->GetCachePath().empty()||state->context->IsSharingWith(CefRequestContext::GetGlobalContext())){FailArtifactOpen(state);return;}
    for(const auto& [_,other]:artifacts)if(state->context->IsSharingWith(other->context)){FailArtifactOpen(state);return;}
    if(!state->context->RegisterSchemeHandlerFactory("http","",new ArtifactFactory(state))||
       !state->context->RegisterSchemeHandlerFactory("https","",new ArtifactFactory(state))){FailArtifactOpen(state);return;}
    artifacts.emplace(state->lease,state);if(!Create(route,0,state->origin+m->GetString("entry").ToString(),state))FailArtifactOpen(state);return;
  }
  if (kind == "open") {auto url=m->GetString("url").ToString();Create(route,0,url.empty()?"about:blank":url,nullptr,m->GetBool("managed"));return; }
  auto p = m->GetDictionary("payload"); if (!p) return;
  auto id = p->GetInt("id"); auto method = p->GetString("method").ToString();
  if (method == "dsh.indicator") { Result(route,id,nullptr,"CEF_FEEDBACK_NOT_IMPLEMENTED"); return; }
  auto args = p->GetList("params");
  if (method == "tab.create") {
    for(const auto& [_,tab]:tabs)if(tab.route==route&&static_cast<Client*>(tab.browser->GetHost()->GetClient().get())->RequiresGuard()){Result(route,id,nullptr,"CEF_HOST_TAB_ADMISSION_REQUIRED");return;}
    for(const auto& [_,state]:artifacts)if(state->route==route){Result(route,id,nullptr,"CEF_ARTIFACT_HOST_ADMISSION_REQUIRED");return;}
    Create(route, id, args->GetDictionary(0)->GetString("url").ToString()); return;
  }
  int tab_id = 0;
  if (method == "tab.close") tab_id = args->GetInt(0);
  else if (args && args->GetSize()) tab_id = args->GetDictionary(0)->GetInt("tabId");
  auto t = tabs.find(tab_id);
  if (t == tabs.end() || t->second.route != route) { Result(route, id, nullptr, "CEF_CROSS_TARGET_REJECTED"); return; }
  if (method == "devtools.attach") { Result(route, id, Empty()); return; }
  if (method == "tab.close") {if(!static_cast<Client*>(t->second.browser->GetHost()->GetClient().get())->GuardValid()){Result(route,id,nullptr,"CEF_DOCUMENT_AUTHORIZATION_CHANGED");return;}static_cast<Client*>(t->second.browser->GetHost()->GetClient().get())->CloseWhenDrained([]{}); Result(route,id,Empty());return; }
  if (method != "devtools.dispatch") { Result(route, id, nullptr, "CEF_ROUTE_METHOD_REJECTED"); return; }
  auto command = args->GetString(1).ToString();
  auto command_params = args->GetSize()>2 ? args->GetDictionary(2) : nullptr;
  if (!Admitted(command, command_params)) {
    Result(route, id, nullptr, "CEF_UNSCOPED_COMMAND_REJECTED"); return;
  }
  auto client = static_cast<Client*>(t->second.browser->GetHost()->GetClient().get());
  client->Dispatch(id, command, args->GetSize()>2 ? args->GetDictionary(2) : nullptr,
      args->GetDictionary(0)->GetString("sessionId").ToString());
}
class App final : public CefApp, public CefBrowserProcessHandler {
public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override { return this; }
  void OnBeforeCommandLineProcessing(const CefString&, CefRefPtr<CefCommandLine> c) override {
    c->RemoveSwitch("remote-debugging-port"); c->RemoveSwitch("remote-debugging-pipe");
  }
  void OnContextInitialized() override {
    auto ready = CefDictionaryValue::Create(); ready->SetString("kind", "ready");
    ready->SetString("generation", generation); ready->SetInt("protocol",1); Write(ready);
#ifdef __APPLE__
    dsh_platform::StartOwnerMonitor([] {
      CefPostTask(TID_UI, base::BindOnce(Shutdown));
    });
#endif
    reader = std::thread([] {
      while (true) {
        uint32_t size = 0;
        if (!Transfer(false, &size, 4) || !size || size > kLimit) break;
        std::string json(size, '\0'); if (!Transfer(false, json.data(), size)) break;
        auto value = CefParseJSON(json, JSON_PARSER_RFC);
        if (!value || !value->GetDictionary()) break;
        CefPostTask(TID_UI, base::BindOnce(Command, value->GetDictionary()));
      }
      if (!stopping) CefPostTask(TID_UI, base::BindOnce(Shutdown));
    });
  }
  IMPLEMENT_REFCOUNTING(App);
};
} // namespace

int RunBroker(CefMainArgs args,void* sandbox){
  int code=CefExecuteProcess(args,nullptr,sandbox);if(code>=0)return code;
#ifdef _WIN32
  if(!sandbox)return 71;
#endif
  auto path=TakeEnvironment("DSH_WORK_BRIDGE_PIPE");auto token=TakeEnvironment("DSH_WORK_BRIDGE_TOKEN");
  generation=TakeEnvironment("DSH_WORK_CEF_GENERATION");auto profile=TakeEnvironment("DSH_WORK_CEF_PROFILE");
  if(path.empty()||token.empty()||generation.empty()||profile.empty())return 72;
  if(!OpenTransport(path)||!Authenticate(token))return 73;
  Erase(token);
  CefSettings settings;CefString(&settings.root_cache_path)=profile;CefString(&settings.cache_path)=profile;settings.remote_debugging_port=0;
#ifdef __APPLE__
  settings.windowless_rendering_enabled=true;
  settings.log_severity=LOGSEVERITY_DISABLE;
#endif
  CefRefPtr<App> app=new App;if(!CefInitialize(args,settings,app,sandbox))return CefGetExitCode();
  CefRunMessageLoop();CancelTransport();if(reader.joinable())reader.join();
#ifdef __APPLE__
  // CEF is still initialized, but the pump and transport reader are joined.
  // The owner notification must never prevent the actual CEF shutdown.
  dsh_platform::PrepareOwnedShutdown();
#endif
  CefShutdown();
#ifdef __APPLE__
  // No CEF calls after this point. The external owner retains the profile
  // through post-root child cleanup; its bounded release is not root exit.
  int owned_exit=dsh_platform::FinishOwnedShutdown();
  CloseTransport();return owned_exit;
#else
  CloseTransport();return 0;
#endif
}
#ifdef _WIN32
CEF_BOOTSTRAP_EXPORT int RunWinMain(HINSTANCE instance,LPWSTR,int,void* sandbox,cef_version_info_t*){return RunBroker(CefMainArgs(instance),sandbox);}
#elif !defined(__APPLE__)
int main(int argc,char** argv){return RunBroker(CefMainArgs(argc,argv),nullptr);}
#endif
