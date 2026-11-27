// Included inside the broker namespace, after the authenticated Write helper.
// CEF's scheme handler is the network boundary; Go is the sole file authority.
#pragma once

class ArtifactResource;
struct ArtifactState {
  std::string lease, partition, origin, route;
  std::atomic_bool revoked{false};
  std::atomic_int browser{0};
  std::atomic_bool admitted{false};
  std::mutex context_mutex;
  CefRefPtr<CefRequestContext> context;
  std::map<ArtifactResource*,CefRefPtr<ArtifactResource>> resources; // active streams; IO owner
  std::atomic_int read_marshaled{0};
  bool draining = false, resources_drained = false; // UI thread only
  int closed_browsers=0,canceled_streams=0;
  std::set<std::string> live_frames;
  int destroyed_frames=0;
  int revocation_frames=0;
};
std::map<std::string,std::shared_ptr<ArtifactState>> artifacts;
std::map<std::string,std::function<void(Dict)>> resource_pending; // IO only
uint64_t resource_serial = 0;
void ArtifactEvent(const std::shared_ptr<ArtifactState>& state,const std::string& kind,
                   int browser=0,const std::string& frame={}) {
  auto m=CefDictionaryValue::Create();m->SetString("kind",kind);
  m->SetString("generation",generation);m->SetString("lease",state->lease);
  m->SetString("partition",state->partition);m->SetInt("browser",browser);
  m->SetString("frame",frame);Write(m);
}
bool ArtifactURL(const std::shared_ptr<ArtifactState>& state,const std::string& url) {
  return url.rfind(state->origin+"/",0)==0;
}
class ArtifactResource final:public CefResourceHandler {
  std::shared_ptr<ArtifactState> state_;
  std::string url_,frame_,pending_,mime_;
  int browser_;
  int64_t size_=0,offset_=0;
  bool canceled_=false;
  CefRefPtr<CefCallback> opened_;
  CefRefPtr<CefResourceReadCallback> reading_;
  void* destination_=nullptr;
  int capacity_=0;
  void Unregister(){CHECK(CefCurrentlyOn(TID_IO));state_->resources.erase(this);}
  void Request(int limit,std::function<void(Dict)> done) {
    CHECK(CefCurrentlyOn(TID_IO));
    pending_=std::to_string(++resource_serial);
    resource_pending.emplace(pending_,std::move(done));
    auto m=CefDictionaryValue::Create();m->SetString("kind","resource");
    m->SetString("generation",generation);m->SetString("lease",state_->lease);
    m->SetString("partition",state_->partition);m->SetInt("browser",browser_);
    m->SetString("frame",frame_);m->SetString("request",pending_);m->SetString("url",url_);
    m->SetString("offset",std::to_string(offset_));m->SetInt("limit",limit);Write(m);
  }
public:
  ArtifactResource(std::shared_ptr<ArtifactState> state,std::string url,int browser,std::string frame)
      :state_(std::move(state)),url_(std::move(url)),frame_(std::move(frame)),browser_(browser) {
    CHECK(CefCurrentlyOn(TID_IO));
    state_->resources.emplace(this,this);
  }
  ~ArtifactResource() override {CHECK(CefCurrentlyOn(TID_IO));CHECK(!state_->resources.contains(this));}
  bool InFlight()const{CHECK(CefCurrentlyOn(TID_IO));return !pending_.empty()||offset_<size_;}
  void BeginMetadata() {
    CHECK(CefCurrentlyOn(TID_IO));
    if(!opened_||!pending_.empty()||!state_->admitted)return;
    if(canceled_||state_->revoked||browser_!=state_->browser){auto cb=opened_;opened_=nullptr;Unregister();cb->Cancel();return;}
    // OnAfterCreated admission has been sent before any resource identity.
    ArtifactEvent(state_,"artifact.frame",browser_,frame_);
    Request(0,[self=CefRefPtr<ArtifactResource>(this)](Dict m){
      self->pending_.clear();auto cb=self->opened_;self->opened_=nullptr;
      if(!cb)return;
      if(self->canceled_||self->state_->revoked||m->HasKey("error")){self->Unregister();cb->Cancel();return;}
      const auto size=m->GetString("size").ToString();
      const auto parsed=std::from_chars(size.data(),size.data()+size.size(),self->size_);
      if(parsed.ec!=std::errc{}||parsed.ptr!=size.data()+size.size()||self->size_<0){self->Unregister();cb->Cancel();return;}
      if(!self->size_)self->Unregister();
      self->mime_=m->GetString("mime");cb->Continue();
    });
  }
  bool Open(CefRefPtr<CefRequest>,bool& handle,CefRefPtr<CefCallback> callback) override {
    handle=false;
    CefPostTask(TID_IO,base::BindOnce([](CefRefPtr<ArtifactResource> self,CefRefPtr<CefCallback> cb){
      if(self->state_->revoked){self->Unregister();cb->Cancel();return;}
      self->opened_=cb;
      self->BeginMetadata();
    },CefRefPtr<ArtifactResource>(this),callback));return true;
  }
  void GetResponseHeaders(CefRefPtr<CefResponse> response,int64_t& length,CefString&) override {
    CHECK(CefCurrentlyOn(TID_IO));
    response->SetStatus(200);response->SetMimeType(mime_.empty()?"application/octet-stream":mime_);
    CefResponse::HeaderMap headers;
    headers.emplace("Cache-Control","no-store");
    headers.emplace("X-Content-Type-Options","nosniff");
    headers.emplace("Content-Security-Policy","default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; worker-src 'none'; frame-src 'self'; object-src 'none'; base-uri 'self'");
    response->SetHeaderMap(headers);length=size_;
  }
  bool Read(void* data,int size,int& read,CefRefPtr<CefResourceReadCallback> callback) override {
    // CEF156 Read is sequenced but may run on arbitrary resource threads.
    // Only its stable buffer/callback cross to the single IO owner.
    read=0;
    if(!CefCurrentlyOn(TID_IO))state_->read_marshaled++;
    if(!CefPostTask(TID_IO,base::BindOnce([](CefRefPtr<ArtifactResource> self,void* data,int size,CefRefPtr<CefResourceReadCallback> cb){self->ReadOnIO(data,size,cb);},CefRefPtr<ArtifactResource>(this),data,size,callback))){read=-2;return false;}
    return true;
  }
  void ReadOnIO(void* data,int size,CefRefPtr<CefResourceReadCallback> callback) {
    CHECK(CefCurrentlyOn(TID_IO));
    if(canceled_||state_->revoked){Unregister();callback->Continue(-2);return;}
    if(offset_>=size_){Unregister();callback->Continue(0);return;}
    destination_=data;capacity_=static_cast<int>(std::min({static_cast<int64_t>(size),int64_t(65536),size_-offset_}));reading_=callback;
    Request(capacity_,[self=CefRefPtr<ArtifactResource>(this)](Dict m){
      self->pending_.clear();auto callback=self->reading_;self->reading_=nullptr;
      if(!callback)return;
      if(self->canceled_||self->state_->revoked||m->HasKey("error")){self->Unregister();callback->Continue(-2);return;}
      auto data=CefBase64Decode(m->GetString("data"));
      const auto count=data?data->GetSize():0;
      if(count>static_cast<size_t>(self->capacity_)){self->Unregister();callback->Continue(-2);return;}
      if(count)data->GetData(self->destination_,count,0);
      self->offset_+=static_cast<int64_t>(count);if(!count||self->offset_>=self->size_)self->Unregister();callback->Continue(static_cast<int>(count));
    });
  }
  void Cancel() override {
    if(CefCurrentlyOn(TID_IO)){CancelOnIO();return;}
    CefPostTask(TID_IO,base::BindOnce([](CefRefPtr<ArtifactResource> self){self->CancelOnIO();},CefRefPtr<ArtifactResource>(this)));
  }
  void CancelOnIO() {
    CHECK(CefCurrentlyOn(TID_IO));
    canceled_=true;
    if(!pending_.empty()){resource_pending.erase(pending_);pending_.clear();}
    if(opened_){auto cb=opened_;opened_=nullptr;cb->Cancel();}
    if(reading_){auto cb=reading_;reading_=nullptr;cb->Continue(-2);}
    destination_=nullptr;
    Unregister();
  }
  IMPLEMENT_REFCOUNTING_DELETE_ON_IOT(ArtifactResource);
};
class DeniedArtifactResource final:public CefResourceHandler {
public:
  bool Open(CefRefPtr<CefRequest>,bool& handle,CefRefPtr<CefCallback>)override{handle=true;return false;}
  void GetResponseHeaders(CefRefPtr<CefResponse>,int64_t&,CefString&)override{}
  void Cancel()override{}
  IMPLEMENT_REFCOUNTING(DeniedArtifactResource);
};
class ArtifactFactory final:public CefSchemeHandlerFactory {
  std::shared_ptr<ArtifactState> state_;
public:
  explicit ArtifactFactory(std::shared_ptr<ArtifactState> state):state_(std::move(state)){}
  CefRefPtr<CefResourceHandler> Create(CefRefPtr<CefBrowser> browser,CefRefPtr<CefFrame> frame,
      const CefString&,CefRefPtr<CefRequest> request) override {
    if(state_->revoked||!browser||!frame||(state_->browser!=0&&browser->GetIdentifier()!=state_->browser)||
       !ArtifactURL(state_,request->GetURL())||request->GetMethod()!="GET")return new DeniedArtifactResource;
    CefRefPtr<CefRequestContext> context;
    {std::lock_guard<std::mutex> lock(state_->context_mutex);context=state_->context;}
    if(!context||!browser->GetHost()->GetRequestContext()->IsSame(context))return new DeniedArtifactResource;
    const auto id=frame->GetIdentifier().ToString();
    return new ArtifactResource(state_,request->GetURL(),browser->GetIdentifier(),id);
  }
  IMPLEMENT_REFCOUNTING(ArtifactFactory);
};
void AdmitArtifactResources(std::shared_ptr<ArtifactState> state) {
  CHECK(CefCurrentlyOn(TID_IO));
  std::vector<CefRefPtr<ArtifactResource>> resources;
  for(const auto& [_,resource]:state->resources)resources.push_back(resource);
  for(auto& resource:resources)resource->BeginMetadata();
}
void ArtifactResponse(Dict m) {
  CHECK(CefCurrentlyOn(TID_IO));
  auto item=resource_pending.extract(m->GetString("request").ToString());
  if(!item.empty())item.mapped()(m);
}
void DrainArtifactResources(std::shared_ptr<ArtifactState> state,base::OnceClosure done) {
  CHECK(CefCurrentlyOn(TID_IO));
  std::vector<CefRefPtr<ArtifactResource>> resources;
  for(const auto& [_,resource]:state->resources)resources.push_back(resource);
  for(auto& resource:resources){if(resource->InFlight())state->canceled_streams++;resource->Cancel();}
  CHECK(state->resources.empty());
  CefPostTask(TID_UI,std::move(done));
}
