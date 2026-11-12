#pragma once

// CEF UI-thread-owned lifetime graph. IDs are Chromium logical child/frame
// tokens; no value here claims to be an OS process instance or an authority.
// document is a broker-local frame load epoch, not Chromium DocumentToken;
// owned_document is the existing main-frame authorization document epoch.
class EngineFrameGraph {
 using Key=std::pair<std::string,int>;
 struct Frame {
  std::string owner,id,parent,document,owned_document;
  int tab=0,browser=0;
  bool main=false,attached=false;
 };
 std::map<std::string,Frame> frames_;
 std::map<Key,std::string> mains_;
 std::map<Key,int> browsers_;
 uint64_t revision_=0,document_=0,fault_epoch_=0;
 void Emit(const std::string& action,const Frame& frame,bool reattached=false,const std::string& previous={}) {
  auto event=CefDictionaryValue::Create();event->SetString("kind","frame.lifecycle");event->SetString("generation",generation);
  event->SetString("revision",std::to_string(++revision_));event->SetString("faultEpoch",std::to_string(fault_epoch_));
  event->SetString("action",action);event->SetString("owner",frame.owner);event->SetInt("tab",frame.tab);event->SetInt("browser",frame.browser);
  event->SetString("frame",frame.id);event->SetString("parent",frame.parent);event->SetString("document",frame.document);event->SetString("ownedDocument",frame.owned_document);
  event->SetBool("main",frame.main);event->SetBool("attached",frame.attached);event->SetBool("reattached",reattached);event->SetString("previous",previous);Write(event);
 }
public:
 void Created(const std::string& owner,int tab,CefRefPtr<CefFrame> frame){
  Frame value;value.owner=owner;value.tab=tab;value.browser=browsers_[Key{owner,tab}];value.id=frame->GetIdentifier().ToString();value.main=frame->IsMain();value.document=std::to_string(++document_);
  if(frame->IsValid()){auto parent=frame->GetParent();if(parent)value.parent=parent->GetIdentifier().ToString();}
  frames_[value.id]=value;Emit("created",value);
 }
 void Bound(const std::string& owner,int tab,int browser){browsers_[Key{owner,tab}]=browser;for(auto& [_,frame]:frames_)if(frame.owner==owner&&frame.tab==tab){frame.browser=browser;Emit("bound",frame);}}
 void Attached(const std::string& owner,CefRefPtr<CefFrame> frame,bool reattached){
  auto it=frames_.find(frame->GetIdentifier().ToString());if(it==frames_.end()||it->second.owner!=owner)return;
  it->second.attached=true;if(frame->IsValid()){auto parent=frame->GetParent();it->second.parent=parent?parent->GetIdentifier().ToString():"";}
  Emit("attached",it->second,reattached);
 }
 void Detached(const std::string& owner,CefRefPtr<CefFrame> frame){auto it=frames_.find(frame->GetIdentifier().ToString());if(it!=frames_.end()&&it->second.owner==owner){it->second.attached=false;Emit("detached",it->second);}}
 void Destroyed(const std::string& owner,CefRefPtr<CefFrame> frame){auto it=frames_.find(frame->GetIdentifier().ToString());if(it!=frames_.end()&&it->second.owner==owner){it->second.attached=false;Emit("destroyed",it->second);frames_.erase(it);}}
 void MainChanged(const std::string& owner,int tab,int browser,CefRefPtr<CefFrame> previous,CefRefPtr<CefFrame> next){
  Frame value;value.owner=owner;value.tab=tab;value.browser=browser;value.main=true;value.id=next?next->GetIdentifier().ToString():"";
  Key key{owner,tab};if(next){mains_[key]=value.id;}else{mains_.erase(key);browsers_.erase(key);}
  Emit("main-changed",value,false,previous?previous->GetIdentifier().ToString():"");
 }
 void Load(const std::string& owner,CefRefPtr<CefFrame> frame){auto it=frames_.find(frame->GetIdentifier().ToString());if(it!=frames_.end()&&it->second.owner==owner){it->second.document=std::to_string(++document_);Emit("document",it->second);}}
 void OwnedDocument(const std::string& owner,int tab,uint64_t document){auto main=mains_.find(Key{owner,tab});if(main==mains_.end())return;auto it=frames_.find(main->second);if(it!=frames_.end()){it->second.owned_document=std::to_string(document);Emit("owned-document",it->second);}}
 uint64_t Fault(){return ++fault_epoch_;}
 uint64_t Revision()const{return revision_;}
};
EngineFrameGraph frame_graph;
