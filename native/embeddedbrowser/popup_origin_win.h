#pragma once
// Windows guest popup origin evidence only. Global CEF includes stay outside
// Client/the broker namespace. There is no guest-visible binding or JS reporter.
#include "include/cef_render_process_handler.h"
#include "include/cef_v8.h"
#include "include/cef_process_message.h"
#include <map>
#include <string>

namespace dsh_popup_origin {
class RenderApp final : public CefApp, public CefRenderProcessHandler {
 struct Snapshot { CefRefPtr<CefV8Context> context; std::string origin,document; };
 std::map<std::string,Snapshot> contexts_;
 uint64_t next_document_=0;
public:
 CefRefPtr<CefRenderProcessHandler> GetRenderProcessHandler()override{return this;}
 void OnContextCreated(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,CefRefPtr<CefV8Context> context)override{
  // CEF invokes this as the context is created, before page scripts. Only the
  // main world belongs to the document; DevTools isolated worlds are ignored.
  auto main=frame->GetV8Context();if(!main||!main->IsSame(context))return;
  if(!context->Enter())return;
  auto value=context->GetGlobal()->GetValue("origin");
  const std::string origin=value&&value->IsString()?value->GetStringValue().ToString():std::string{};
  context->Exit();
  // Capture the pristine Blink getter once. Later Object.defineProperty or
  // window.origin assignments are website state and can never change evidence.
  contexts_[frame->GetIdentifier().ToString()]={context,origin,std::to_string(++next_document_)};
 }
 void OnContextReleased(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,CefRefPtr<CefV8Context> context)override{
  auto it=contexts_.find(frame->GetIdentifier().ToString());
  if(it==contexts_.end()||!it->second.context->IsSame(context))return;
  auto message=CefProcessMessage::Create("dsh.popup.origin.released");message->GetArgumentList()->SetString(0,it->second.document);
  contexts_.erase(it);frame->SendProcessMessage(PID_BROWSER,message);
 }
 bool OnProcessMessageReceived(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,CefProcessId source,CefRefPtr<CefProcessMessage> message)override{
  if(source!=PID_BROWSER||message->GetName()!="dsh.popup.origin.query")return false;
  auto args=message->GetArgumentList();if(!args||args->GetSize()!=1||args->GetType(0)!=VTYPE_STRING)return true;
  auto it=contexts_.find(frame->GetIdentifier().ToString());
  auto reply=CefProcessMessage::Create("dsh.popup.origin.result");auto values=reply->GetArgumentList();values->SetString(0,args->GetString(0));
  if(it!=contexts_.end()&&it->second.context->IsValid()){
   values->SetString(1,it->second.origin);values->SetString(2,it->second.document);
  }else{values->SetString(1,"");values->SetString(2,"");}
  // The browser receives CEF's real sender frame, not an id chosen by JS.
  frame->SendProcessMessage(PID_BROWSER,reply);return true;
 }
 IMPLEMENT_REFCOUNTING(RenderApp);
};
}
