// Members of Client. UI-thread owned; only CEF engine events create pending.
struct DownloadPending {
 std::string id,request,frame,loader,url,source,document,scope,view,presentation,mime,name;
 std::chrono::steady_clock::time_point expires;
};
std::map<std::string,std::pair<std::string,std::string>> download_frames_;
std::map<int,std::string> download_commands_;
std::map<std::string,DownloadPending> download_pending_;
bool download_ready_=false;
void DownloadCommand(const std::string& method,Dict params){
 if(!tabs.contains(tab_))return;auto id=++next_command_;download_commands_[id]=method;
 // DevTools observers disallow recursive dispatch. Keep FIFO order but return
 // from the observer before entering its agent again.
 CefPostTask(TID_UI,base::BindOnce([](CefRefPtr<Client> self,int id,std::string method,Dict params){
  if(self->closing_||!tabs.contains(self->tab_)){self->download_commands_.erase(id);return;}
  tabs.at(self->tab_).browser->GetHost()->ExecuteDevToolsMethod(id,method,params);
 },CefRefPtr<Client>(this),id,method,params));
}
void DownloadRememberFrames(Dict tree){
 if(!tree)return;auto f=tree->GetDictionary("frame");if(f)download_frames_[f->GetString("id").ToString()]={f->GetString("url"),f->GetString("loaderId")};
 auto children=tree->GetList("childFrames");if(children)for(size_t n=0;n<children->GetSize();++n)DownloadRememberFrames(children->GetDictionary(n));
}
void CancelDownloadPending(){
 if(download_pending_.empty())return;download_pending_.clear();
 auto e=CefDictionaryValue::Create();e->SetString("kind","download.invalidated");e->SetString("generation",generation);e->SetString("owner",owner_route_);Write(e);
}
bool DownloadOwner(dsh_file_picker::Owner& peer){
 if(!managed_||artifact_||!last_present_args_||!tabs.contains(tab_)||!ViewVisible())return false;
 auto guest=tabs.at(tab_).browser->GetHost()->GetWindowHandle();auto root=GetAncestor(guest,GA_ROOT);
 if(!root||root==guest)return false;
 peer={root,GetParent(guest),guest,static_cast<DWORD>(last_present_args_->GetInt("pid")),0};
 try{peer.started=std::stoull(last_present_args_->GetString("started").ToString());if(reinterpret_cast<HWND>(std::stoull(last_present_args_->GetString("parent").ToString()))!=peer.parent)return false;}catch(...){return false;}
 auto process=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE,FALSE,peer.pid);
 bool ok=dsh_file_picker::SameOwner(peer,process);if(process)CloseHandle(process);return ok;
}
bool DownloadValid(const DownloadPending& p){
 auto f=download_frames_.find(p.frame);dsh_file_picker::Owner owner{};
 return !closing_&&!control_blocked&&download_ready_&&ViewVisible()&&
  p.document==std::to_string(document_epoch_)&&p.scope==std::to_string(scope_epoch_)&&p.view==view_lease_&&p.presentation==presentation_epoch_&&
  f!=download_frames_.end()&&f->second.first==p.source&&f->second.second==p.loader&&
  std::chrono::steady_clock::now()<p.expires&&DownloadOwner(owner);
}
bool DownloadIntercept(const std::string& method,Dict params){
 if(!params)return false;
 if(method=="Page.frameNavigated"){
  auto frame=params->GetDictionary("frame");if(frame){CancelDownloadPending();download_frames_[frame->GetString("id").ToString()]={frame->GetString("url"),frame->GetString("loaderId")};}
 }else if(method=="Page.frameDetached"){CancelDownloadPending();download_frames_.erase(params->GetString("frameId").ToString());}
 if(method!="Fetch.requestPaused")return false;
 auto request=params->GetDictionary("request");const auto request_id=params->GetString("requestId").ToString();
 auto headers=params->GetList("responseHeaders");bool attachment=false;std::string mime;int64_t total=-1;
 if(headers)for(size_t n=0;n<headers->GetSize();++n){
  auto h=headers->GetDictionary(n);if(!h)continue;auto name=h->GetString("name").ToString(),value=h->GetString("value").ToString();
  std::transform(name.begin(),name.end(),name.begin(),[](unsigned char c){return static_cast<char>(std::tolower(c));});
  if(name=="content-disposition"){std::string lower=value;std::transform(lower.begin(),lower.end(),lower.begin(),[](unsigned char c){return static_cast<char>(std::tolower(c));});attachment=lower.rfind("attachment",0)==0;}
  if(name=="content-type")mime=value;
  if(name=="content-length"){try{total=std::stoll(value);}catch(...){}}
 }
 auto resolution=CefDictionaryValue::Create();resolution->SetString("requestId",request_id);
 // This first product slice recognizes actual attachment navigations, not XHR.
 // Other native/PDF/download-attribute paths remain explicitly denied.
 if(!attachment||params->GetString("resourceType")!="Document"){DownloadCommand("Fetch.continueRequest",resolution);return true;}
 resolution->SetString("errorReason","Aborted");DownloadCommand("Fetch.failRequest",resolution);
 dsh_file_picker::Owner peer{};auto frame=params->GetString("frameId").ToString();auto source=download_frames_.find(frame);
 if(!request||request->GetString("method")!="GET"||request->HasKey("postData")||request->GetBool("hasPostData")||!download_ready_||!download_pending_.empty()||
    source==download_frames_.end()||DownloadOrigin(source->second.first).empty()||DownloadOrigin(request->GetString("url")).empty()||!DownloadOwner(peer))return true;
 DownloadPending p;p.id=DownloadNonce();if(p.id.empty())return true;
 p.request=request_id;p.frame=frame;p.loader=source->second.second;p.source=source->second.first;p.url=request->GetString("url");p.mime=mime;
 p.document=std::to_string(document_epoch_);p.scope=std::to_string(scope_epoch_);p.view=view_lease_;p.presentation=presentation_epoch_;p.expires=std::chrono::steady_clock::now()+std::chrono::minutes(2);
 // The website suggestion is advisory; Go normalizes it and the system Save
 // dialog chooses the directory. No page-supplied destination is accepted.
 CefURLParts url_parts;if(CefParseURL(p.url,url_parts)){auto path=CefString(&url_parts.path).ToString();auto slash=path.find_last_of('/');p.name=slash==std::string::npos?path:path.substr(slash+1);}
 download_pending_[p.id]=p;
 auto e=CefDictionaryValue::Create();e->SetString("kind","download.pending");e->SetString("generation",generation);e->SetString("owner",owner_route_);
 e->SetString("pending",p.id);e->SetString("requestId",p.request);e->SetString("frameId",p.frame);e->SetString("document",p.document+"/"+p.scope+"/"+p.loader);
 e->SetString("presentation",p.presentation);e->SetString("source",p.source);e->SetString("sourceOrigin",DownloadOrigin(p.source));e->SetString("url",p.url);e->SetString("mime",p.mime);e->SetString("name",p.name);e->SetString("total",std::to_string(total));
 e->SetString("parent",std::to_string(reinterpret_cast<uintptr_t>(peer.root)));e->SetInt("pid",static_cast<int>(peer.pid));e->SetString("started",std::to_string(peer.started));Write(e);
 return true;
}
bool DownloadResult(int id,Dict response){
 auto command=download_commands_.find(id);if(command==download_commands_.end())return false;
 auto method=command->second;download_commands_.erase(command);
 if(response->HasKey("error")){download_ready_=false;CancelDownloadPending();return true;}
 if(method=="Fetch.enable")download_ready_=true;
 if(method=="Page.getFrameTree"&&response->GetDictionary("result"))DownloadRememberFrames(response->GetDictionary("result")->GetDictionary("frameTree"));
 return true;
}
Dict DownloadPendingState(Dict args){
 if(!args)return nullptr;auto it=download_pending_.find(args->GetString("pending").ToString());if(it==download_pending_.end()||!DownloadValid(it->second))return nullptr;
 auto result=CefDictionaryValue::Create();result->SetBool("valid",true);return result;
}
void DownloadDismiss(Dict args){if(args)download_pending_.erase(args->GetString("pending").ToString());}
std::string DownloadStart(Dict args){
 HANDLE file=INVALID_HANDLE_VALUE;
 try{if(args)file=reinterpret_cast<HANDLE>(std::stoull(args->GetString("writer").ToString()));}catch(...){return "DOWNLOAD_WRITER_REQUIRED";}
 const auto id=args?args->GetString("id").ToString():std::string{};
 if(auto known=download_writers.find(id);known!=download_writers.end()){
  if(file!=known->second.value&&file!=INVALID_HANDLE_VALUE&&file)CloseHandle(file);
  return "DOWNLOAD_APPROVAL_EXPIRED";
 }
 auto reject=[file,id](std::string error){if(file!=INVALID_HANDLE_VALUE&&file)CloseHandle(file);if(!id.empty())download_writers[id]={file,true};return error;};
 if(!args||file==INVALID_HANDLE_VALUE||!file||GetFileType(file)!=FILE_TYPE_DISK)return reject("DOWNLOAD_WRITER_REQUIRED");
 auto it=download_pending_.find(args->GetString("pending").ToString());
 if(id.empty()||download_transfers.contains(id)||it==download_pending_.end()||!DownloadValid(it->second))return reject("DOWNLOAD_APPROVAL_EXPIRED");
 auto p=it->second;uint64_t limit=0,reserve=0;try{limit=std::stoull(args->GetString("maxBytes").ToString());reserve=std::stoull(args->GetString("minFreeBytes").ToString());}catch(...){return reject("DOWNLOAD_BUDGET_REQUIRED");}
 if(!limit||args->GetString("volume").empty())return reject("DOWNLOAD_BUDGET_REQUIRED");
 download_pending_.erase(it); // One shot, consumed before starting any new GET.
 download_writers[id]={file,false};
 auto transfer=CefRefPtr<DownloadTransfer>(new DownloadTransfer(id,owner_route_,p.url,tabs.at(tab_).browser->GetHost()->GetRequestContext(),file,limit,reserve,args->GetString("volume")));
 download_transfers[id]=transfer;transfer->Start();return {};
}
