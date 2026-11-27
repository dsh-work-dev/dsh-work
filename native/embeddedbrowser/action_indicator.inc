// Client-owned, fixed renderer operations. No source/expression enters the wire.
std::map<int,std::function<void(Dict)>> feedback_commands_;
struct FeedbackCommit {std::string coordinates,token;uint64_t document,epoch;};
std::map<int,FeedbackCommit> feedback_commits_;
struct FeedbackInputCall {int id;std::string method;Dict params;std::string session,token;uint64_t document,epoch;};
std::vector<FeedbackInputCall> feedback_inputs_;
bool feedback_press_pending_=false;
void FeedbackRejectInputs(){auto queued=std::move(feedback_inputs_);feedback_inputs_.clear();feedback_press_pending_=false;for(auto& call:queued)Result(route_,call.id,nullptr,"CEF_CLICK_INTERRUPTED");}
void FeedbackFlushInputs(){feedback_press_pending_=false;auto queued=std::move(feedback_inputs_);feedback_inputs_.clear();for(auto& call:queued){if(call.token!=guard_token_||call.document!=document_epoch_||call.epoch!=feedback_epoch_)Result(route_,call.id,nullptr,"CEF_CLICK_INTERRUPTED");else Dispatch(call.id,call.method,call.params,call.session);}}

std::string feedback_session_,feedback_target_;
// Privacy of a root self-attach is distinct from ownership by a consumer.
std::set<std::string> feedback_sessions_,feedback_retired_sessions_,feedback_private_roots_,feedback_private_detached_roots_;
int feedback_context_=0;
uint64_t feedback_epoch_=0,feedback_attempt_=0;
bool feedback_attach_expired_=false,feedback_attaching_=false,feedback_heartbeat_=false,feedback_active_=false,feedback_running_=false,feedback_click_=false,feedback_starting_=false;
std::function<void(bool)> feedback_waiter_;
static double FeedbackNumber(Dict value,const char* key){return value->GetType(key)==VTYPE_INT?value->GetInt(key):value->GetDouble(key);}
static std::string FeedbackJSON(Dict value){auto v=CefValue::Create();v->SetDictionary(value);return CefWriteJSON(v,JSON_WRITER_DEFAULT).ToString();}
bool FeedbackCommand(const std::string& method,Dict params,std::function<void(Dict)> done={},const std::string& session={},bool retain_late=false){
 if(!tabs.contains(tab_)||closing_){if(done)done(nullptr);return false;}
 const int id=++next_command_;auto m=CefDictionaryValue::Create();m->SetInt("id",id);m->SetString("method",method);if(params)m->SetDictionary("params",params);if(!session.empty())m->SetString("sessionId",session);
 feedback_commands_[id]=std::move(done);auto self=CefRefPtr<Client>(this);
 CefPostDelayedTask(TID_UI,base::BindOnce([](CefRefPtr<Client> self,int id,bool retain_late){auto it=self->feedback_commands_.find(id);if(it==self->feedback_commands_.end())return;auto done=it->second;if(!retain_late)self->feedback_commands_.erase(it);if(done)done(nullptr);},self,id,retain_late),600);
 auto json=FeedbackJSON(m);
 if(!tabs.at(tab_).browser->GetHost()->SendDevToolsMessage(json.data(),json.size())){auto callback=std::move(feedback_commands_[id]);feedback_commands_.erase(id);if(callback)callback(nullptr);return false;}
 return true;
}
void FeedbackEvaluate(const std::string& expression,std::function<void(Dict)> done={},bool promise=false){
 if(!feedback_context_||feedback_session_.empty()){if(done)done(nullptr);return;}
 auto args=CefDictionaryValue::Create();args->SetInt("contextId",feedback_context_);args->SetString("expression",expression);args->SetBool("returnByValue",true);args->SetBool("awaitPromise",promise);
 FeedbackCommand("Runtime.evaluate",args,std::move(done),feedback_session_);
}
void FeedbackCancel(bool dispose=false){
 ++feedback_attempt_;FeedbackRejectInputs();
 ++feedback_epoch_;feedback_starting_=false;feedback_heartbeat_=false;feedback_active_=false;feedback_running_=feedback_click_=false;
 if(feedback_context_)FeedbackEvaluate(dispose?"globalThis.__dshIndicator?.dispose()":"globalThis.__dshIndicator?.update({active:false})");
 auto waiter=std::move(feedback_waiter_);feedback_waiter_={};if(waiter)waiter(false);
 if(dispose)feedback_context_=0;
 else if(feedback_context_){feedback_heartbeat_=true;FeedbackHeartbeat(feedback_epoch_);}
}
void FeedbackInstall(std::function<void(bool)> done){
 if(feedback_context_){done(true);return;}
 if(feedback_session_.empty()||feedback_starting_){done(false);return;}
 feedback_starting_=true;const auto epoch=feedback_epoch_;auto self=CefRefPtr<Client>(this);
 FeedbackCommand("Page.getFrameTree",nullptr,[self,epoch,done](Dict response){
  auto result=response?response->GetDictionary("result"):nullptr;auto tree=result?result->GetDictionary("frameTree"):nullptr;auto frame=tree?tree->GetDictionary("frame"):nullptr;
  if(epoch!=self->feedback_epoch_){done(false);return;}
  if(!frame){self->feedback_starting_=false;done(false);return;}
  auto args=CefDictionaryValue::Create();args->SetString("frameId",frame->GetString("id"));args->SetString("worldName","dsh-work-action-indicator-v1");
  self->FeedbackCommand("Page.createIsolatedWorld",args,[self,epoch,done](Dict response){
   auto result=response?response->GetDictionary("result"):nullptr;
   if(epoch!=self->feedback_epoch_){done(false);return;}
   if(!result){self->feedback_starting_=false;done(false);return;}
   self->feedback_context_=result->GetInt("executionContextId");
   self->FeedbackEvaluate(kActionIndicatorInstall,[self,epoch,done](Dict response){if(epoch!=self->feedback_epoch_){done(false);return;}self->feedback_starting_=false;done(response&&!response->HasKey("error"));});
  },self->feedback_session_);
 },feedback_session_);
}
void FeedbackHeartbeat(uint64_t epoch){
 if(epoch!=feedback_epoch_||!feedback_context_||closing_)return;
 auto args=CefDictionaryValue::Create();args->SetBool("active",feedback_active_);args->SetBool("running",feedback_active_&&feedback_running_);
 FeedbackEvaluate("globalThis.__dshIndicator?.update("+FeedbackJSON(args)+")");
 auto self=CefRefPtr<Client>(this);CefPostDelayedTask(TID_UI,base::BindOnce([](CefRefPtr<Client> self,uint64_t epoch){self->FeedbackHeartbeat(epoch);},self,epoch),1000);
}
void FeedbackState(Dict state,std::function<void(bool)> done){
 feedback_running_=state&&state->GetBool("running");feedback_click_=feedback_running_&&state->GetString("action")=="browser_click";
 const auto action=state?state->GetString("action").ToString():std::string{};
 const bool target_action=action=="browser_click"||action=="browser_hover"||action=="browser_drag"||action=="browser_type"||action=="browser_fill_form"||action=="browser_select_option"||action=="browser_press_key";
 if(feedback_running_&&!target_action){feedback_running_=false;done(true);return;}
 // Reads never initialize the renderer or wait for animation.
 if(!feedback_running_){FeedbackEvaluate("globalThis.__dshIndicator?.update({running:false})");done(true);return;}
 if(!GuardValid()||!ViewVisible()){done(false);return;}
 feedback_active_=true;const auto epoch=feedback_epoch_;auto self=CefRefPtr<Client>(this);
 auto ready=[self,state,epoch,done](bool ok){if(!ok||epoch!=self->feedback_epoch_||!self->feedback_active_){done(false);return;}
  auto hint=CefDictionaryValue::Create();hint->SetBool("active",true);hint->SetBool("running",self->feedback_running_);
  auto rect=state->GetDictionary("rect");if(rect){auto clean=CefDictionaryValue::Create();bool valid=true;for(const auto& key:{"x","y","width","height"}){auto type=rect->GetType(key);if(type!=VTYPE_DOUBLE&&type!=VTYPE_INT){valid=false;break;}auto n=FeedbackNumber(rect,key);if(!std::isfinite(n)){valid=false;break;}clean->SetDouble(key,n);}if(valid)hint->SetDictionary("rect",clean);}
  self->FeedbackEvaluate("globalThis.__dshIndicator?.update("+FeedbackJSON(hint)+")");if(!self->feedback_heartbeat_){self->feedback_heartbeat_=true;self->FeedbackHeartbeat(epoch);}done(true);
 };
 if(feedback_session_.empty()){
  if(feedback_attaching_){done(false);return;}
  feedback_attaching_=true;
  FeedbackCommand("Target.getTargetInfo",nullptr,[self,ready,epoch](Dict response){if(epoch!=self->feedback_epoch_){self->feedback_attaching_=false;ready(false);return;}auto result=response?response->GetDictionary("result"):nullptr;auto info=result?result->GetDictionary("targetInfo"):nullptr;if(!info){self->feedback_attaching_=false;ready(false);return;}
   self->feedback_target_=info->GetString("targetId");auto args=CefDictionaryValue::Create();args->SetString("targetId",self->feedback_target_);args->SetBool("flatten",true);
   // Only one attach can be unresolved, including after its caller timeout.
   // Keep its exact command callback until the engine replies or Client closes.
   self->feedback_attaching_=true;self->feedback_attach_expired_=false;
   auto notified=std::make_shared<bool>(false);
   bool accepted=self->FeedbackCommand("Target.attachToTarget",args,[self,ready,epoch,notified](Dict response){
    auto notify=[ready,notified](bool ok){if(!*notified){*notified=true;ready(ok);}};
    if(!response){self->feedback_attach_expired_=true;notify(false);return;}
    auto result=response->GetDictionary("result");auto session=result?result->GetString("sessionId").ToString():std::string{};
    const bool retired=self->feedback_attach_expired_||epoch!=self->feedback_epoch_||self->feedback_retired_sessions_.contains(session)||self->feedback_private_detached_roots_.contains(session);
    self->feedback_attaching_=false;
    if(retired){if(!session.empty()){self->feedback_retired_sessions_.insert(session);auto args=CefDictionaryValue::Create();args->SetString("sessionId",session);self->FeedbackCommand("Target.detachFromTarget",args);}notify(false);return;}
    if(session.empty()){notify(false);return;}
    self->feedback_session_=session;self->feedback_sessions_.insert(session);self->FeedbackInstall(notify);
   },{},true);
   if(!accepted)self->feedback_attaching_=false;
  });
 }else FeedbackInstall(ready);
}
bool FeedbackResult(Dict response){
 auto it=feedback_commands_.find(response->GetInt("id"));if(it==feedback_commands_.end())return false;
 auto callback=std::move(it->second);feedback_commands_.erase(it);if(callback)callback(response);return true;
}
// Record privacy before consumer routing, but never infer Feedback ownership
// from the shared engine target. DownloadOrigin self-attaches to it as well.
void FeedbackRememberPrivateRoot(const std::string& method,Dict params,const std::string& session){
 if(method=="Target.detachedFromTarget"&&params){const auto child=params->GetString("sessionId").ToString();if(feedback_private_roots_.contains(child))feedback_private_detached_roots_.insert(child);return;}
 if(method!="Target.attachedToTarget"||!params||!session.empty()||feedback_target_.empty())return;
 auto info=params->GetDictionary("targetInfo");auto child=params->GetString("sessionId").ToString();
 // The SDK wire cannot issue Target.attachToTarget. A root self-attachment to
 // this trusted engine target is Host-private; auto-attached child targets are not.
 if(info&&info->GetString("targetId")==feedback_target_&&!child.empty())feedback_private_roots_.insert(child);
}
bool FeedbackEvent(const std::string& method,Dict params,const std::string& session){
 if(method=="Target.detachedFromTarget"&&params){const auto detached=params->GetString("sessionId").ToString();
  if(feedback_sessions_.contains(detached)||feedback_retired_sessions_.contains(detached)){
   feedback_sessions_.erase(detached);feedback_retired_sessions_.insert(detached);
   if(detached==feedback_session_){feedback_context_=0;feedback_session_.clear();FeedbackCancel(true);}
   return true;
  }
 }
 return feedback_sessions_.contains(session)||feedback_retired_sessions_.contains(session);
}
bool FeedbackPrivateEvent(const std::string& method,Dict params,const std::string& session){
 // Run only after both concrete consumers; this filter never takes ownership,
 // retires, or detaches a DownloadOrigin session.
 if(feedback_private_roots_.contains(session))return true;
 if(params&&(method=="Target.attachedToTarget"||method=="Target.detachedFromTarget"))return feedback_private_roots_.contains(params->GetString("sessionId").ToString());
 return false;
}
void FeedbackInput(const std::string& method,Dict params){
 if(!feedback_active_||!params||method!="Input.dispatchMouseEvent")return;
 if(feedback_click_&&params->GetString("type")=="mouseMoved")return; // Click's reveal belongs to prepareClick.
 auto hint=CefDictionaryValue::Create();hint->SetBool("active",true);hint->SetBool("running",feedback_running_);hint->SetDouble("x",FeedbackNumber(params,"x"));hint->SetDouble("y",FeedbackNumber(params,"y"));
 FeedbackEvaluate("globalThis.__dshIndicator?.update("+FeedbackJSON(hint)+")");
}
void FeedbackBeforeClick(int id,Dict params,const std::string& session){
 if(feedback_waiter_){Result(route_,id,nullptr,"CEF_CLICK_INTERRUPTED");return;}
 const auto epoch=feedback_epoch_,document=document_epoch_,attempt=++feedback_attempt_;const auto token=guard_token_;
 auto self=CefRefPtr<Client>(this);auto saved=params->Copy(false);
 feedback_waiter_=[self,id,saved,session,epoch,document,token](bool ready){
  if(!ready||epoch!=self->feedback_epoch_||document!=self->document_epoch_||token!=self->guard_token_||!self->GuardValid()||!self->ViewVisible()){Result(self->route_,id,nullptr,"CEF_CLICK_INTERRUPTED");self->FeedbackRejectInputs();self->FlushRevocation();return;}
  self->feedback_press_pending_=true;self->Dispatch(id,"Input.dispatchMouseEvent",saved,session,true);
 };
 CefPostDelayedTask(TID_UI,base::BindOnce([](CefRefPtr<Client> self,uint64_t epoch,uint64_t attempt){if(epoch==self->feedback_epoch_&&attempt==self->feedback_attempt_&&self->feedback_waiter_){self->FeedbackEvaluate("globalThis.__dshIndicator?.cancelClick()");auto done=std::move(self->feedback_waiter_);self->feedback_waiter_={};++self->feedback_attempt_;done(false);}},self,epoch,attempt),450);
 FeedbackInstall([self,epoch,attempt,saved](bool ok){
  if(epoch!=self->feedback_epoch_||attempt!=self->feedback_attempt_||!self->feedback_waiter_)return;
  if(!ok){auto done=std::move(self->feedback_waiter_);self->feedback_waiter_={};++self->feedback_attempt_;done(false);return;}
  auto coordinate=std::to_string(FeedbackNumber(saved,"x"))+","+std::to_string(FeedbackNumber(saved,"y"));
  self->FeedbackEvaluate("globalThis.__dshIndicator?.update({active:true,running:true});(()=>{const p=globalThis.__dshIndicator?.prepareClick("+coordinate+",350);return p?.status==='prepared'?globalThis.__dshIndicator.waitClick(p.token):{status:'cancelled'}})()",[self,epoch,attempt](Dict response){
   if(epoch!=self->feedback_epoch_||attempt!=self->feedback_attempt_||!self->feedback_waiter_)return;
   auto result=response?response->GetDictionary("result"):nullptr;auto remote=result?result->GetDictionary("result"):nullptr;auto value=remote?remote->GetDictionary("value"):nullptr;
   auto done=std::move(self->feedback_waiter_);self->feedback_waiter_={};++self->feedback_attempt_;done(value&&value->GetString("status")=="ready");
  },true);
 });
}
