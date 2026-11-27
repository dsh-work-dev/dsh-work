#include "presenter_mac.h"
#include "../../../internal/localipc/mach_peer.h"
#include "include/cef_parser.h"
#include "include/wrapper/cef_closure_task.h"
#include <CommonCrypto/CommonHMAC.h>
#include <sys/file.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/socket.h>
#include <fcntl.h>
#include <poll.h>
#include <unistd.h>
#include <array>
#include <atomic>
#include <charconv>
#include <chrono>
#include <cmath>
#include <condition_variable>
#include <cstring>
#include <map>
#include <memory>
#include <mutex>
#include <thread>

namespace {
using Dict=CefRefPtr<CefDictionaryValue>;
using Clock=std::chrono::steady_clock;
struct FrameHeader{uint64_t magic,sequence,capacity;uint32_t width,height;};
constexpr uint64_t kFrameMagic=0x4453484d41434631ULL;
constexpr size_t kFrameBudget=256U*1024U*1024U;
static_assert(sizeof(FrameHeader)==32);
bool Bytes(int fd,bool writing,void* data,size_t size,Clock::time_point end){auto* p=(char*)data;while(size){auto left=std::chrono::duration_cast<std::chrono::milliseconds>(end-Clock::now()).count();if(left<=0)return false;pollfd event{fd,(short)(writing?POLLOUT:POLLIN),0};int n=poll(&event,1,(int)left);if(n<0&&errno==EINTR)continue;if(n<=0)return false;auto count=writing?send(fd,p,size,MSG_DONTWAIT):recv(fd,p,size,MSG_DONTWAIT);if(count<0&&(errno==EINTR||errno==EAGAIN||errno==EWOULDBLOCK))continue;if(count<=0)return false;p+=count;size-=count;}return true;}
bool Frame(int fd,bool writing,std::string& text){auto end=Clock::now()+std::chrono::seconds(3);uint32_t size=writing?(uint32_t)text.size():0;if(!Bytes(fd,writing,&size,4,end)||!size||size>16384)return false;if(!writing)text.resize(size);return Bytes(fd,writing,text.data(),size,end);}
std::string JSON(Dict d){auto value=CefValue::Create();value->SetDictionary(d);return CefWriteJSON(value,JSON_WRITER_DEFAULT).ToString();}
Dict Parse(const std::string& s){auto value=CefParseJSON(s,JSON_PARSER_RFC);return value&&value->GetType()==VTYPE_DICTIONARY?value->GetDictionary():nullptr;}
double Numeric(Dict d,const char* key){return d->GetType(key)==VTYPE_INT?d->GetInt(key):d->GetDouble(key);}
bool Authenticate(int fd,const std::string& token){std::array<unsigned char,32> nonce{},actual{},answer{};auto end=Clock::now()+std::chrono::seconds(3);if(!Bytes(fd,false,nonce.data(),32,end))return false;auto mac=[&](const char* role){std::string data=role;data.append((char*)nonce.data(),32);CCHmac(kCCHmacAlgSHA256,token.data(),token.size(),data.data(),data.size(),answer.data());};mac("worker");if(!Bytes(fd,true,answer.data(),32,end)||!Bytes(fd,false,actual.data(),32,end))return false;mac("host");unsigned char diff=0;for(size_t i=0;i<32;i++)diff|=actual[i]^answer[i];return diff==0;}
template<class T>bool Number(const std::string& s,T& out){auto [p,e]=std::from_chars(s.data(),s.data()+s.size(),out);return e==std::errc()&&p==s.data()+s.size()&&out>0;}
struct Link;
std::map<int,std::shared_ptr<Link>> links; // CEF UI thread owns map and browser API.
struct Link:std::enable_shared_from_this<Link>{
 int fd=-1,frame_fd=-1;void* memory=nullptr;size_t bytes=0;
 CefRefPtr<CefBrowser> browser;std::function<void()> manual;
 std::string view,epoch,endpoint,path;uint64_t next=0;int width=1024,height=768;double scale=1;
 std::atomic<bool> connected{false},visible{false},physical_visible{false},pending_hidden{false},stopped{false};
 std::mutex mu,write_mu;std::condition_variable cv;std::string waiting;Dict reply;std::thread reader;
 ~Link(){Close();}
 void Close(){stopped=true;visible=false;if(fd>=0)shutdown(fd,SHUT_RDWR);cv.notify_all();if(reader.joinable()&&reader.get_id()!=std::this_thread::get_id())reader.join();if(fd>=0)close(fd);fd=-1;if(memory)munmap(memory,bytes);memory=nullptr;if(frame_fd>=0)close(frame_fd);frame_fd=-1;}
 bool Write(Dict d){std::lock_guard lock(write_mu);auto text=JSON(d);return !stopped&&Frame(fd,true,text);}
 Dict Request(Dict d){std::unique_lock lock(mu);waiting=std::to_string(++next);d->SetString("id",waiting);reply=nullptr;lock.unlock();if(!Write(d))return nullptr;lock.lock();cv.wait_for(lock,std::chrono::seconds(3),[&]{return reply||!connected;});auto value=reply;waiting.clear();reply=nullptr;return value;}
 bool Connect(const std::string& token,uint32_t pid,uint64_t started){int result=EIO;dsh_ipc_peer peer{};std::thread auth([&]{result=dsh_ipc_connect(endpoint.c_str(),-1,&fd,&peer);int one=1;if(!result&&setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one)))result=errno;if(!result&&(peer.pid!=pid||peer.started!=started||!Authenticate(fd,token)))result=EACCES;});auth.join();if(result)return false;int one=1;setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one));connected=true;reader=std::thread([self=shared_from_this()]{self->Read();});return true;}
 void Read(){
  while(!stopped){
   // A healthy idle stream has no deadline. Cancellation shuts down this FD;
   // bounded Frame reads apply only after at least one byte is available.
   pollfd p{fd,POLLIN,0};int n=poll(&p,1,100);if(n<0&&errno==EINTR)continue;if(n==0)continue;if(n<0)break;
   std::string text;if(!Frame(fd,false,text))break;auto d=Parse(text);if(!d)break;
   if(d->GetString("op")=="ack") {std::lock_guard lock(mu);if(!waiting.empty()&&d->GetString("id")==waiting){reply=d;cv.notify_all();}continue;}
   if(d->GetString("op")=="visibility"&&d->GetString("view")==view&&!d->GetBool("visible"))pending_hidden=true;
   CefPostTask(TID_UI,base::BindOnce([](std::shared_ptr<Link> self,Dict event){self->Input(event);},shared_from_this(),d));
  }
  connected=false;visible=false;cv.notify_all();
 }
 bool Map(Dict ack){
  auto candidate=ack->GetString("path").ToString();int size=ack->GetInt("bytes");double dpr=Numeric(ack,"scale");
  auto slash=endpoint.rfind('/');if(slash==std::string::npos||candidate.rfind(endpoint.substr(0,slash)+"/frame-",0)!=0||size<(int)sizeof(FrameHeader)||(size_t)size>sizeof(FrameHeader)+kFrameBudget||!std::isfinite(dpr)||dpr<1||dpr>4)return false;
  if(candidate==path&&bytes==(size_t)size){scale=dpr;return true;}
  int owned=open(candidate.c_str(),O_RDWR|O_NOFOLLOW|O_CLOEXEC);struct stat st{};
  if(owned<0||fstat(owned,&st)||!S_ISREG(st.st_mode)||st.st_uid!=getuid()||(st.st_mode&0777)!=0600||st.st_size!=size){if(owned>=0)close(owned);return false;}
  void* mapped=mmap(nullptr,size,PROT_READ|PROT_WRITE,MAP_SHARED,owned,0);if(mapped==MAP_FAILED){close(owned);return false;}
  auto* h=(FrameHeader*)mapped;if(h->magic!=kFrameMagic||h->capacity!=(size_t)size-sizeof(FrameHeader)){munmap(mapped,size);close(owned);return false;}
  if(memory)munmap(memory,bytes);if(frame_fd>=0)close(frame_fd);memory=mapped;bytes=size;frame_fd=owned;path=candidate;scale=dpr;return true;
 }
 void Input(Dict d){
  auto found=links.find(browser->GetIdentifier());if(stopped||!connected||!visible||found==links.end()||found->second.get()!=this||!browser->IsValid()||d->GetString("view")!=view||d->GetString("epoch")!=epoch)return;
  auto op=d->GetString("op").ToString();auto h=browser->GetHost();
  if(op=="visibility"){physical_visible=d->GetBool("visible");if(!physical_visible&&manual)manual();h->WasHidden(!physical_visible);if(physical_visible)h->Invalidate(PET_VIEW);return;}
  if(op=="focus"){h->SetFocus(d->GetBool("focused"));return;}
  if(op!="mouse"&&op!="key"&&op!="text"&&op!="composition"&&op!="cancel-composition")return;
  if(manual)manual();
  if(op=="mouse") {CefMouseEvent e;e.x=(int)Numeric(d,"x");e.y=(int)Numeric(d,"y");e.modifiers=d->GetInt("modifiers");if(e.x<0||e.y<0||e.x>width||e.y>height)return;auto kind=d->GetString("kind");if(kind=="move")h->SendMouseMoveEvent(e,false);else if(kind=="wheel")h->SendMouseWheelEvent(e,d->GetInt("dx"),d->GetInt("dy"));else if(kind=="down"||kind=="up"){int b=d->GetInt("button");if(b<0||b>2)return;h->SendMouseClickEvent(e,b==1?MBT_RIGHT:b==2?MBT_MIDDLE:MBT_LEFT,kind=="up",std::min(3,std::max(1,d->GetInt("count"))));}}
  else if(op=="key") {int type=d->GetInt("type");if(type<0||type>3)return;CefKeyEvent e;e.type=(cef_key_event_type_t)type;e.windows_key_code=d->GetInt("vk");e.native_key_code=d->GetInt("native");e.character=(char16_t)d->GetInt("character");e.unmodified_character=(char16_t)d->GetInt("unmodified");e.modifiers=d->GetInt("modifiers");h->SendKeyEvent(e);}
  else if(op=="text")h->ImeCommitText(d->GetString("text"),CefRange(-1,-1),0);
  else if(op=="composition"){CefString text=d->GetString("text");int from=d->GetInt("from"),to=d->GetInt("to");if(from<0||to<from||(size_t)to>text.length())return;CefCompositionUnderline line;line.range=CefRange(0,text.length());line.color=0xff000000;h->ImeSetComposition(text,{line},CefRange(-1,-1),CefRange(from,to));}
  else h->ImeCancelComposition();
 }
};
std::shared_ptr<Link> Find(int id){auto found=links.find(id);return found==links.end()?nullptr:found->second;}
}
namespace dsh_platform {
std::string PresentBrowser(CefRefPtr<CefBrowser> browser,Dict args,std::function<void()> manual){
 if(!browser||!browser->IsValid()||!args)return "CEF_MAC_PRESENT_REJECTED";auto bounds=args->GetDictionary("bounds");uint32_t pid=0;uint64_t started=0;
 auto endpoint=args->GetString("presenterEndpoint").ToString(),token=args->GetString("presenterToken").ToString(),view=args->GetString("view").ToString(),epoch=args->GetString("epoch").ToString();
 if(!bounds||endpoint.empty()||endpoint.size()>103||token.size()<32||view.size()<16||epoch.empty()||!Number(args->GetString("started").ToString(),started)||args->GetInt("pid")<=0)return "CEF_MAC_PRESENT_REJECTED";pid=(uint32_t)args->GetInt("pid");
 auto link=Find(browser->GetIdentifier());if(link&&(link->endpoint!=endpoint||link->view!=view)){RetireBrowser(browser->GetIdentifier());link=nullptr;}
 if(!link){link=std::make_shared<Link>();link->browser=browser;link->view=view;link->endpoint=endpoint;link->manual=std::move(manual);if(!link->Connect(token,pid,started)){link->Close();return "CEF_MAC_PRESENTER_PEER_REJECTED";}links[browser->GetIdentifier()]=link;}
 auto request=CefDictionaryValue::Create();request->SetString("op","present");request->SetString("view",view);request->SetString("epoch",epoch);request->SetInt("browser",browser->GetIdentifier());request->SetDictionary("bounds",bounds->Copy(false));request->SetBool("visible",args->GetBool("visible"));
 auto ack=link->Request(request);if(!ack||!ack->GetString("error").empty()||ack->GetString("view")!=view||ack->GetString("epoch")!=epoch||!link->Map(ack)){RetireBrowser(browser->GetIdentifier());return "CEF_MAC_PRESENT_ACK_FAILED";}
 link->physical_visible=ack->GetBool("visible");link->epoch=epoch;link->width=std::max(1,bounds->GetInt("Width"));link->height=std::max(1,bounds->GetInt("Height"));link->visible=args->GetBool("visible")&&bounds->GetInt("Width")>0&&bounds->GetInt("Height")>0;
 browser->GetHost()->WasHidden(!(link->visible&&link->physical_visible));browser->GetHost()->NotifyScreenInfoChanged();browser->GetHost()->WasResized();if(link->visible)browser->GetHost()->Invalidate(PET_VIEW);return {};
}
std::string HideBrowser(CefRefPtr<CefBrowser> browser,Dict args){auto link=Find(browser->GetIdentifier());if(!link)return {};auto epoch=args?args->GetString("epoch").ToString():link->epoch;auto request=CefDictionaryValue::Create();request->SetString("op","hide");request->SetString("view",link->view);request->SetString("epoch",epoch);auto ack=link->Request(request);if(!ack||!ack->GetString("error").empty())return "CEF_MAC_HIDE_ACK_FAILED";link->epoch=epoch;link->visible=false;browser->GetHost()->WasHidden(true);browser->GetHost()->SetFocus(false);return {};}
bool BrowserVisible(CefRefPtr<CefBrowser> browser){
 auto link=Find(browser->GetIdentifier());if(!link||!link->connected||!link->visible||link->stopped)return false;
 // Final dispatch asks the trusted Cocoa host, rather than reusing the last
 // slot proposal or waiting for its asynchronous window visibility event.
 auto d=CefDictionaryValue::Create();d->SetString("op","state");d->SetString("view",link->view);d->SetString("epoch",link->epoch);auto ack=link->Request(d);
 bool actual=ack&&ack->GetString("error").empty()&&ack->GetString("view")==link->view&&ack->GetString("epoch")==link->epoch&&ack->GetBool("visible");
 bool invalidated=link->pending_hidden.exchange(false);
 link->physical_visible=actual;if((invalidated||!actual)&&link->manual)link->manual();browser->GetHost()->WasHidden(!actual);return actual&&!invalidated;
}
void RetireBrowser(int id){auto link=Find(id);if(!link)return;links.erase(id);link->Close();}
void BrowserViewRect(int id,CefRect& rect){auto link=Find(id);rect=CefRect(0,0,link?link->width:1024,link?link->height:768);}
bool BrowserScreenInfo(int id,CefScreenInfo& info){auto link=Find(id);if(!link)return false;info.device_scale_factor=(float)link->scale;info.depth=32;info.depth_per_component=8;info.is_monochrome=false;info.rect=info.available_rect=CefRect(0,0,link->width,link->height);return true;}
void BrowserPaint(int id,CefRenderHandler::PaintElementType type,const void* buffer,int width,int height){auto link=Find(id);if(!link||!link->connected||!link->visible||!link->memory||type!=PET_VIEW||width<=0||height<=0||width>8192||height>8192)return;size_t count=(size_t)width*height*4;if(count>link->bytes-sizeof(FrameHeader)||flock(link->frame_fd,LOCK_EX|LOCK_NB))return;auto* h=(FrameHeader*)link->memory;memcpy((char*)link->memory+sizeof(FrameHeader),buffer,count);h->width=width;h->height=height;h->sequence++;if(!h->sequence)h->sequence++;flock(link->frame_fd,LOCK_UN);}
void BrowserIME(int id,const CefRange& range,const CefRenderHandler::RectList& rects){auto link=Find(id);if(!link||!link->visible)return;auto d=CefDictionaryValue::Create();d->SetString("op","ime");d->SetString("id","ime");d->SetString("view",link->view);d->SetString("epoch",link->epoch);d->SetInt("From",range.from);d->SetInt("To",range.to);if(!rects.empty()){auto r=rects.front();d->SetDouble("X",r.x);d->SetDouble("Y",r.y);d->SetDouble("Width",r.width);d->SetDouble("Height",r.height);}link->Write(d);}
}
