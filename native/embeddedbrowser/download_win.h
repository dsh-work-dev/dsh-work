#pragma once
// CEF owns HTTP/TLS/cookies and request cancellation. The sole custom data
// adapter writes an already-approved, write-only duplicated stage handle.
// Header dependencies are included by broker.cc at global scope: this file
// deliberately lives inside its anonymous namespace.
class DownloadTransfer;
std::map<std::string,CefRefPtr<DownloadTransfer>> download_transfers;
struct DownloadWriter { HANDLE value; bool closed; };
std::map<std::string,DownloadWriter> download_writers;
void DownloadTransferFinished(const std::string& id);
inline void DownloadDiscardWriter(CefRefPtr<CefDictionaryValue> args){
 if(!args)return;
 try{
  auto id=args->GetString("id").ToString();
  auto h=reinterpret_cast<HANDLE>(std::stoull(args->GetString("writer").ToString()));
  if(!download_writers.contains(id)){
   if(h&&h!=INVALID_HANDLE_VALUE)CloseHandle(h);
   download_writers[id]={h,true};
  }
 }catch(...){}
}

inline std::string DownloadNonce(){
 unsigned char bytes[20];if(BCryptGenRandom(nullptr,bytes,sizeof(bytes),BCRYPT_USE_SYSTEM_PREFERRED_RNG)!=0)return {};
 static constexpr char hex[]="0123456789abcdef";std::string result;for(auto b:bytes){result+=hex[b>>4];result+=hex[b&15];}return result;
}
inline std::string DownloadOrigin(const std::string& url){
 CefURLParts p;if(!CefParseURL(url,p))return {};auto scheme=CefString(&p.scheme).ToString();
 if((scheme!="http"&&scheme!="https")||CefString(&p.host).empty()||!CefString(&p.username).empty()||!CefString(&p.password).empty())return {};
 auto origin=CefString(&p.origin).ToString();if(!origin.empty()&&origin.back()=='/')origin.pop_back();return origin;
}
class DownloadTransfer final:public CefURLRequestClient{
 std::string id_,owner_,url_,origin_,volume_,error_;CefRefPtr<CefRequestContext> context_;
 CefRefPtr<CefURLRequest> request_;HANDLE file_=INVALID_HANDLE_VALUE;
 uint64_t limit_,reserve_,written_=0,total_=0;unsigned redirects_=0;bool done_=false,canceling_=false;std::atomic_bool drained_{false},cancel_requested_{false};
 std::mutex cancel_mutex_;
 std::chrono::steady_clock::time_point last_progress_{};
 void Emit(bool finished){
  auto e=CefDictionaryValue::Create();e->SetString("kind",finished?"download.finished":"download.progress");e->SetString("generation",generation);
  e->SetString("id",id_);e->SetString("owner",owner_);e->SetString("received",std::to_string(written_));e->SetString("total",std::to_string(total_));
  if(finished){e->SetBool("success",error_.empty()&&!canceling_);if(!error_.empty())e->SetString("error",error_);else if(canceling_)e->SetString("error","DOWNLOAD_CANCELED");}
  Write(e);
 }
 void Finish(std::string error){
  if(done_)return;done_=true;if(error_.empty())error_=std::move(error);
  request_=nullptr;
  if(file_!=INVALID_HANDLE_VALUE){if(!FlushFileBuffers(file_)&&error_.empty())error_="DOWNLOAD_WRITE_FAILED";CloseHandle(file_);file_=INVALID_HANDLE_VALUE;}
  drained_=true;Emit(true);auto id=id_;CefPostTask(TID_UI,base::BindOnce(DownloadTransferFinished,id));
 }
 void Begin(){
  std::lock_guard<std::mutex> lock(cancel_mutex_);
  if(done_)return;if(cancel_requested_.load()){CancelOnThread();return;}
  auto r=CefRequest::Create();r->SetURL(url_);r->SetMethod("GET");
  r->SetFlags(UR_FLAG_SKIP_CACHE|UR_FLAG_ALLOW_STORED_CREDENTIALS|UR_FLAG_STOP_ON_REDIRECT|UR_FLAG_NO_RETRY_ON_5XX);
  request_=CefURLRequest::Create(r,this,context_);if(!request_)Finish("DOWNLOAD_REQUEST_FAILED");
 }
 void CancelOnThread(){if(done_)return;canceling_=true;if(request_)request_->Cancel();else Finish("DOWNLOAD_CANCELED");}
public:
 DownloadTransfer(std::string id,std::string owner,std::string url,CefRefPtr<CefRequestContext> context,HANDLE file,uint64_t limit,uint64_t reserve,std::string volume):
 id_(std::move(id)),owner_(std::move(owner)),url_(std::move(url)),origin_(DownloadOrigin(url_)),volume_(std::move(volume)),context_(context),file_(file),limit_(limit),reserve_(reserve){}
 void Start(){CefPostTask(TID_FILE_USER_VISIBLE,base::BindOnce([](CefRefPtr<DownloadTransfer> self){self->Begin();},CefRefPtr<DownloadTransfer>(this)));}
 bool Drained()const{return drained_.load();}
 void Cancel(){{std::lock_guard<std::mutex> lock(cancel_mutex_);cancel_requested_=true;}CefPostTask(TID_FILE_USER_VISIBLE,base::BindOnce([](CefRefPtr<DownloadTransfer> self){self->CancelOnThread();},CefRefPtr<DownloadTransfer>(this)));}
 void OnRequestComplete(CefRefPtr<CefURLRequest> r)override{
  if(done_)return;auto response=r->GetResponse();int status=response?response->GetStatus():0;
  // STOP_ON_REDIRECT completes as UR_CANCELED in locked CEF. Explicit
  // cancellation wins even if its FILE-thread task is still queued.
  if(cancel_requested_.load()){canceling_=true;Finish("DOWNLOAD_CANCELED");return;}
  if(!canceling_&&error_.empty()&&r->GetRequestStatus()==UR_CANCELED&&status>=300&&status<400){
   CefString resolved;auto location=response->GetHeaderByName("Location");
   if(++redirects_<=5&&!location.empty()&&CefResolveURL(url_,location,resolved)&&DownloadOrigin(resolved)==origin_){url_=resolved;request_=nullptr;Begin();return;}
   Finish("DOWNLOAD_REDIRECT_NOT_APPROVED");return;
  }
  Finish(!error_.empty()?error_:(canceling_?"DOWNLOAD_CANCELED":r->GetRequestStatus()==UR_SUCCESS&&status==200?"":status>=400?"DOWNLOAD_HTTP_FAILED":"DOWNLOAD_NETWORK_FAILED"));
 }
 void OnUploadProgress(CefRefPtr<CefURLRequest>,int64_t,int64_t)override{}
 void OnDownloadProgress(CefRefPtr<CefURLRequest>,int64_t,int64_t total)override{if(total>0)total_=static_cast<uint64_t>(total);}
 void OnDownloadData(CefRefPtr<CefURLRequest> r,const void* data,size_t n)override{
  if(done_||canceling_||cancel_requested_.load()||!error_.empty())return;
  auto response=r->GetResponse();if(!response||response->GetStatus()!=200)return;
  if(n>limit_-written_){error_="DOWNLOAD_BUDGET_EXCEEDED";r->Cancel();return;}
  ULARGE_INTEGER free{};if(!GetDiskFreeSpaceExW(CefString(volume_).ToWString().c_str(),&free,nullptr,nullptr)||free.QuadPart<reserve_||n>free.QuadPart-reserve_){error_="DOWNLOAD_DISK_SPACE_REQUIRED";r->Cancel();return;}
  size_t offset=0;
  while(offset<n){DWORD count=static_cast<DWORD>(std::min<size_t>(n-offset,1<<20)),actual=0;
   if(!WriteFile(file_,static_cast<const char*>(data)+offset,count,&actual,nullptr)||actual==0){error_="DOWNLOAD_WRITE_FAILED";r->Cancel();return;}
   offset+=actual;written_+=actual;
  }
  auto now=std::chrono::steady_clock::now();if(now-last_progress_>=std::chrono::milliseconds(250)){last_progress_=now;Emit(false);}
 }
 bool GetAuthCredentials(bool,const CefString&,int,const CefString&,const CefString&,CefRefPtr<CefAuthCallback>)override{return false;}
private:IMPLEMENT_REFCOUNTING(DownloadTransfer);
};
inline void DownloadTransferFinished(const std::string& id){
 if(auto writer=download_writers.find(id);writer!=download_writers.end())writer->second.closed=true;
 download_transfers.erase(id);
 if(stopping&&tabs.empty()&&artifacts.empty()&&closing_clients.empty()&&download_transfers.empty())CefQuitMessageLoop();
}
