// Members of Client. CEF UI-thread owned. This denies the engine window; the
// only approved effect is a one-shot URL dispatch through the source DSH tab.
struct PopupPending {
 Dict identity;
 CefRefPtr<CefFrame> frame;
 uint64_t document,frame_document;
 bool origin_verified=false;
 std::chrono::steady_clock::time_point expires;
};
std::vector<PopupPending> popup_pending_;
uint64_t next_popup_=0;
std::map<std::string,uint64_t> popup_frame_epochs_;
bool popup_suspended_=false,popup_overlay_visible_=false;
bool PopupOwnerVisible(){
 if(closing_||control_blocked||!view_visible_||!managed_||artifact_||!last_present_args_||!tabs.contains(tab_))return false;
 auto guest=tabs.at(tab_).browser->GetHost()->GetWindowHandle();auto root=GetAncestor(guest,GA_ROOT);
 if(!root||root==guest)return false;
 dsh_file_picker::Owner peer{root,GetParent(guest),guest,static_cast<DWORD>(last_present_args_->GetInt("pid")),0};
 try{peer.started=std::stoull(last_present_args_->GetString("started").ToString());if(reinterpret_cast<HWND>(std::stoull(last_present_args_->GetString("parent").ToString()))!=peer.parent)return false;}catch(...){return false;}
 auto process=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE,FALSE,peer.pid);
 bool ok=dsh_file_picker::SameOwner(peer,process)&&dsh_platform::Visible(peer.parent)&&
   (popup_suspended_?!dsh_platform::Visible(guest):dsh_platform::Visible(guest));
 if(process)CloseHandle(process);return ok;
}
bool PopupSourceValid(const PopupPending& p){
 return PopupOwnerVisible()&&p.frame&&p.frame->IsValid()&&p.document==document_epoch_&&p.frame_document==popup_frame_epochs_[p.identity->GetString("frame").ToString()]&&
  p.identity->GetString("view")==view_lease_&&p.identity->GetString("sourceURL")==p.frame->GetURL()&&
  std::chrono::steady_clock::now()<p.expires;
}
bool RestorePopup(){
 bool ok=true;
 if(popup_suspended_&&view_visible_&&!closing_&&tabs.contains(tab_)&&last_present_args_){
  auto error=dsh_platform::Present(tabs.at(tab_).browser->GetHost()->GetWindowHandle(),last_present_args_);
  dialog_restore_error_=error;ok=error.empty();
 }
 popup_suspended_=false;popup_overlay_visible_=false;return ok;
}
void CancelPopups(bool restore){
 popup_pending_.clear();if(restore)RestorePopup();else{popup_suspended_=false;popup_overlay_visible_=false;}
}
Dict PendingPopup(){
 if(popup_pending_.empty())return nullptr;
 if(!PopupSourceValid(popup_pending_.front())){CancelPopups(true);return nullptr;}
 if(!popup_pending_.front().origin_verified)return nullptr;
 return popup_pending_.front().identity->Copy(false);
}
bool PopupVisible(){return popup_suspended_&&popup_overlay_visible_&&PendingPopup();}
void QueuePopup(CefRefPtr<CefBrowser> browser,CefRefPtr<CefFrame> frame,int popup_id,const std::string& url,bool gesture){
 // No Host window is created for a rejected request. Source origin comes only
 // from CEF's initiating frame, not the target name, referrer, or page script.
 if(!gesture||!frame||!frame->IsValid()||!browser||!tabs.contains(tab_)||browser->GetIdentifier()!=tabs.at(tab_).browser->GetIdentifier()||
  !ViewVisible()||!loaded_||artifact_||dialog_pending_||file_picker_||url.size()>8192)return;
 CefURLParts parts;if(!CefParseURL(url,parts))return;
 auto scheme=CefString(&parts.scheme).ToString();
 if((scheme!="http"&&scheme!="https")||CefString(&parts.host).empty()||!CefString(&parts.username).empty()||!CefString(&parts.password).empty())return;
 // Bound bursts; rejected overflow never becomes an untracked engine window.
 PendingPopup();if(popup_pending_.size()>=8)return;
 auto value=CefDictionaryValue::Create();
 value->SetString("id",generation+"/"+std::to_string(tab_)+"/"+std::to_string(++next_popup_));
 value->SetString("browser",std::to_string(browser->GetIdentifier()));value->SetString("popup",std::to_string(popup_id));
 value->SetString("frame",frame->GetIdentifier());value->SetString("document",std::to_string(document_epoch_));
 value->SetString("view",view_lease_);value->SetString("presentation",presentation_epoch_);
 value->SetString("origin","");value->SetString("sourceURL",frame->GetURL());value->SetString("url",url);
 value->SetBool("userGesture",gesture);
 const auto frame_document=popup_frame_epochs_[frame->GetIdentifier().ToString()];
 value->SetString("frameDocument",std::to_string(frame_document));value->SetString("context","");
 popup_pending_.push_back({value,frame,document_epoch_,frame_document,false,std::chrono::steady_clock::now()+std::chrono::seconds(60)});
 auto query=CefProcessMessage::Create("dsh.popup.origin.query");query->GetArgumentList()->SetString(0,value->GetString("id"));
 frame->SendProcessMessage(PID_RENDERER,query);
}
// Source document changes invalidate only requests from that real frame.
// Main-document changes invalidate all descendants, regardless of frame reuse.
void PopupFrameChanged(CefRefPtr<CefFrame> frame){
 const auto id=frame->GetIdentifier().ToString();++popup_frame_epochs_[id];
 if(frame->IsMain()){CancelPopups(true);return;}
 const bool displayed=!popup_pending_.empty()&&popup_pending_.front().identity->GetString("frame")==id;
 std::erase_if(popup_pending_,[&](const PopupPending& p){return p.identity->GetString("frame")==id;});
 if(displayed)RestorePopup();
}
bool PopupOriginMessage(CefRefPtr<CefBrowser> browser,CefRefPtr<CefFrame> frame,CefProcessId source,CefRefPtr<CefProcessMessage> message){
 if(source!=PID_RENDERER||!frame||!browser||!tabs.contains(tab_)||browser->GetIdentifier()!=tabs.at(tab_).browser->GetIdentifier())return false;
 auto args=message->GetArgumentList();if(!args)return false;
 const auto frame_id=frame->GetIdentifier().ToString();
 if(message->GetName()=="dsh.popup.origin.released"){
  if(args->GetSize()!=1||args->GetType(0)!=VTYPE_STRING)return true;
  const auto context=args->GetString(0);
  const bool displayed=!popup_pending_.empty()&&popup_pending_.front().identity->GetString("frame")==frame_id&&popup_pending_.front().identity->GetString("context")==context;
  std::erase_if(popup_pending_,[&](const PopupPending& p){return p.identity->GetString("frame")==frame_id&&p.identity->GetString("context")==context;});
  if(displayed)RestorePopup();return true;
 }
 if(message->GetName()!="dsh.popup.origin.result")return false;
 if(args->GetSize()!=3||args->GetType(0)!=VTYPE_STRING||args->GetType(1)!=VTYPE_STRING||args->GetType(2)!=VTYPE_STRING)return true;
 const auto id=args->GetString(0),context=args->GetString(2);const auto origin=args->GetString(1).ToString();
 auto it=std::find_if(popup_pending_.begin(),popup_pending_.end(),[&](const PopupPending& p){return p.identity->GetString("id")==id&&p.identity->GetString("frame")==frame_id;});
 if(it==popup_pending_.end())return true;
 // URL parsing validates the engine serialization, never derives its origin.
 if(!PopupSourceValid(*it)||context.empty()||origin.empty()||origin=="null"||
   (origin.rfind("http://",0)!=0&&origin.rfind("https://",0)!=0)||Origin(origin)!=origin){
  const bool displayed=it==popup_pending_.begin();popup_pending_.erase(it);if(displayed)RestorePopup();return true;
 }
 it->identity->SetString("origin",origin);it->identity->SetString("context",context);it->origin_verified=true;return true;
}
Dict PopupAction(Dict args){
 auto expected=args?args->GetDictionary("popup"):nullptr,actual=PendingPopup();
 if(!actual||!expected||!actual->IsEqual(expected))return nullptr;
 const auto action=args->GetString("action").ToString();auto result=CefDictionaryValue::Create();
 auto guest=tabs.at(tab_).browser->GetHost()->GetWindowHandle();
 if(action=="suspend"){
  if(!dsh_platform::Hide(guest).empty())return nullptr;
  popup_suspended_=true;popup_overlay_visible_=false;return result;
 }
 if(action=="ready"){
  if(!popup_suspended_||!PopupOwnerVisible())return nullptr;
  popup_overlay_visible_=true;return result;
 }
 if((action!="allow"&&action!="deny")||!PopupVisible())return nullptr;
 // Consume before restoring native presentation or returning the URL: neither
 // a repeated answer nor a lost IPC reply can replay the opening effect.
 const auto url=actual->GetString("url");popup_pending_.erase(popup_pending_.begin());
 if(!RestorePopup()){CancelPopups(false);return nullptr;}
 if(action=="allow")result->SetString("url",url);
 return result;
}
