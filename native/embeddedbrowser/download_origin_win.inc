// Members of Client. A separate official DevTools session observes origin/frame
// metadata only; SDK Runtime.enable/disable keep their real engine semantics.
struct DownloadCommandInfo { std::string method,session; uint64_t revision; };
struct DownloadFrame { std::string url,loader; uint64_t revision=0; };
struct DownloadContext { std::string session,unique,origin,loader; int id=0; uint64_t revision=0; };
std::map<std::string,DownloadFrame> download_frames_;
std::map<std::string,DownloadContext> download_contexts_;
std::set<std::string> download_origin_sessions_,download_origin_retired_sessions_;
// CEF's native lifetime fence only, never an origin/CDP-frame correlation.
// Never-committed speculative navigation frames are not source documents.
// Keep commit history across detach/reattach; remove only on destruction.
std::set<std::string> download_committed_frames_;
std::string download_origin_target_,download_origin_session_;
uint64_t download_observation_revision_=0;
void DownloadOriginObserved(const std::string& state,const std::string& method,int code=0){
 auto event=CefDictionaryValue::Create();event->SetString("kind","origin.observed");event->SetString("generation",generation);
 event->SetString("owner",owner_route_);event->SetString("scope",state);event->SetString("method",method);event->SetInt("code",code);Write(event);
}
void DownloadOriginRetire(){
 CancelDownloadPending();download_contexts_.clear();download_frames_.clear();
 download_origin_retired_sessions_.insert(download_origin_sessions_.begin(),download_origin_sessions_.end());download_origin_sessions_.clear();
 download_origin_session_.clear();++download_observation_revision_;
}
void DownloadOriginClearSession(const std::string& session){
 bool changed=false;
 for(auto it=download_contexts_.begin();it!=download_contexts_.end();){if(it->second.session==session){it=download_contexts_.erase(it);changed=true;}else ++it;}
 if(changed)CancelDownloadPending();++download_observation_revision_;
}
void DownloadSetFrame(Dict frame,uint64_t revision){
 if(!frame)return;const auto id=frame->GetString("id").ToString(),url=frame->GetString("url").ToString(),loader=frame->GetString("loaderId").ToString();
 if(id.empty()||loader.empty())return;
 auto it=download_frames_.find(id);if(it!=download_frames_.end()&&it->second.revision>revision)return;
 if(it!=download_frames_.end()&&(it->second.loader!=loader||it->second.url!=url)){
  download_contexts_.erase(id);CancelDownloadPending();
 }
 download_frames_[id]={url,loader,revision};
}
void DownloadRememberFrames(Dict tree,uint64_t revision){
 if(!tree)return;DownloadSetFrame(tree->GetDictionary("frame"),revision);
 auto children=tree->GetList("childFrames");if(children)for(size_t n=0;n<children->GetSize();++n)DownloadRememberFrames(children->GetDictionary(n),revision);
}
void DownloadOriginInitSession(const std::string& session){
 if(closing_||session.empty()||!download_origin_sessions_.insert(session).second)return;
 DownloadCommand("Page.enable",CefDictionaryValue::Create(),session);
 // OOPIF targets get their own observer session; never pause page execution.
 auto attach=CefDictionaryValue::Create();attach->SetBool("autoAttach",true);attach->SetBool("waitForDebuggerOnStart",false);attach->SetBool("flatten",true);
 auto filter=CefListValue::Create();auto iframe=CefDictionaryValue::Create();iframe->SetString("type","iframe");iframe->SetBool("exclude",false);filter->SetDictionary(0,iframe);auto rest=CefDictionaryValue::Create();rest->SetBool("exclude",true);filter->SetDictionary(1,rest);attach->SetList("filter",filter);
 DownloadCommand("Target.setAutoAttach",attach,session);
}
bool DownloadOriginResult(const DownloadCommandInfo& command,Dict response){
 const auto& method=command.method;const auto& session=command.session;
 if(method!="Target.getTargetInfo"&&method!="Target.attachToTarget"&&session.empty())return false;
 if(closing_)return true;
 // Child teardown can leave responses in flight. A retired child's response
 // has no authority to retire the independent, still-healthy root observer.
 if(!session.empty()&&!download_origin_sessions_.contains(session))return true;
 if(method=="Target.detachFromTarget")return true;
 if(response->HasKey("error")){
  auto error=response->GetDictionary("error");DownloadOriginObserved("failed",method,error?error->GetInt("code"):0);
  if(!session.empty()&&session!=download_origin_session_){DownloadOriginClearSession(session);download_origin_retired_sessions_.insert(session);download_origin_sessions_.erase(session);}
  else DownloadOriginRetire();return true;
 }
 auto result=response->GetDictionary("result");if(!result)return true;
 if(method=="Target.getTargetInfo"){
  auto info=result->GetDictionary("targetInfo");download_origin_target_=info?info->GetString("targetId").ToString():std::string{};
  if(download_origin_target_.empty()){DownloadOriginObserved("failed",method);return true;}
  auto args=CefDictionaryValue::Create();args->SetString("targetId",download_origin_target_);args->SetBool("flatten",true);
  DownloadCommand("Target.attachToTarget",args);
 }else if(method=="Target.attachToTarget"){
  download_origin_session_=result->GetString("sessionId");DownloadOriginInitSession(download_origin_session_);
 }else if(!download_origin_sessions_.contains(session))return true;
 else if(method=="Page.enable")DownloadCommand("Page.getFrameTree",CefDictionaryValue::Create(),session);
 else if(method=="Page.getFrameTree"){
  DownloadRememberFrames(result->GetDictionary("frameTree"),command.revision);
  DownloadCommand("Runtime.enable",CefDictionaryValue::Create(),session);
 }else if(method=="Runtime.enable")DownloadOriginObserved("ready",method);
 return true;
}
bool DownloadOriginEvent(const std::string& method,Dict params,const std::string& session){
 if(!params)return !session.empty()&&download_origin_retired_sessions_.contains(session);
 if(download_origin_retired_sessions_.contains(session)){
  // Auto-attach was already in flight when its private parent retired. Extend
  // the exact private tombstone tree before dropping this envelope, otherwise
  // the child's later flattened events could look like unrelated SDK events.
  if(method=="Target.attachedToTarget"&&params->GetType("sessionId")==VTYPE_STRING){
   const auto child=params->GetString("sessionId").ToString();if(!child.empty())download_origin_retired_sessions_.insert(child);
  }
  return true;
 }
 if(method=="Target.detachedFromTarget"&&download_origin_retired_sessions_.contains(params->GetString("sessionId").ToString()))return true;
 if(method=="Target.attachedToTarget"){
  auto info=params->GetDictionary("targetInfo");const auto child=params->GetString("sessionId").ToString();
  // The only root-level internal attachment is to this exact browser target.
  if(session.empty()&&!download_origin_target_.empty()&&info&&info->GetString("targetId")==download_origin_target_)return true;
  if(download_origin_sessions_.contains(session)){
   if(info&&info->GetString("type")=="iframe")DownloadOriginInitSession(child);
   else{download_origin_retired_sessions_.insert(child);auto args=CefDictionaryValue::Create();args->SetString("sessionId",child);DownloadCommand("Target.detachFromTarget",args,session);}
   return true;
  }
 }
 if(method=="Target.detachedFromTarget"&&download_origin_sessions_.contains(params->GetString("sessionId").ToString())){
  const auto child=params->GetString("sessionId").ToString();DownloadOriginClearSession(child);download_origin_retired_sessions_.insert(child);download_origin_sessions_.erase(child);
  if(child==download_origin_session_){DownloadOriginObserved("detached",method);DownloadOriginRetire();}return true;
 }
 if(!download_origin_sessions_.contains(session))return false;
 if(method=="Page.frameNavigated")DownloadSetFrame(params->GetDictionary("frame"),++download_observation_revision_);
 else if(method=="Page.navigatedWithinDocument"){
  const auto frame=params->GetString("frameId").ToString();auto current=download_frames_.find(frame);
  if(current!=download_frames_.end()){current->second.url=params->GetString("url");current->second.revision=++download_observation_revision_;}
  if(std::any_of(download_pending_.begin(),download_pending_.end(),[&](const auto& pending){return pending.second.frame==frame;}))CancelDownloadPending();
 }
 else if(method=="Page.frameDetached"){
  const auto frame=params->GetString("frameId").ToString();download_frames_.erase(frame);download_contexts_.erase(frame);++download_observation_revision_;CancelDownloadPending();
 }else if(method=="Runtime.executionContextCreated"){
  auto context=params->GetDictionary("context");auto aux=context?context->GetDictionary("auxData"):nullptr;
  if(!context||!aux||aux->GetType("isDefault")!=VTYPE_BOOL||!aux->GetBool("isDefault"))return true;
  const auto frame=aux->GetString("frameId").ToString(),unique=context->GetString("uniqueId").ToString(),origin=context->GetString("origin").ToString();
  auto source=download_frames_.find(frame);download_contexts_.erase(frame);CancelDownloadPending();
  // Opaque V8 context origins serialize as ://. Only canonical HTTP(S) tuples
  // are accepted; Page.Frame.securityOrigin and frame URL are never authority.
  if(source==download_frames_.end()||unique.empty()||context->GetInt("id")<=0||origin.empty()||DownloadOrigin(origin)!=origin){DownloadOriginObserved("rejected-context",method);return true;}
  download_contexts_[frame]={session,unique,origin,source->second.loader,context->GetInt("id"),++download_observation_revision_};
  DownloadOriginObserved("context",method);
 }else if(method=="Runtime.executionContextDestroyed"){
  const auto unique=params->GetString("executionContextUniqueId").ToString();bool changed=false;
  for(auto it=download_contexts_.begin();it!=download_contexts_.end();){
   if(it->second.session==session&&(unique.empty()||it->second.unique==unique)){it=download_contexts_.erase(it);changed=true;}else ++it;
  }
  if(changed)CancelDownloadPending();++download_observation_revision_;
 }else if(method=="Runtime.executionContextsCleared")DownloadOriginClearSession(session);
 return true; // all internal session events stay out of the SDK/Agent channel
}
