#include "presentation_window.h"
#include <iostream>
#include <vector>
using namespace dsh_presenter;
struct FakeWindowOps {
 std::string failure;LONG_PTR style=WS_POPUP|WS_CAPTION|WS_VISIBLE,ex=WS_EX_NOACTIVATE|WS_EX_WINDOWEDGE;HWND held_parent=nullptr;RECT rect{};bool moved=false;
 bool valid(HWND w){return w!=nullptr;}
 HWND parent(HWND){return failure=="parent-readback"&&moved?nullptr:held_parent;}
 HWND root(HWND w){return w;}
 DWORD pid(HWND){return 7;}
 bool read(HWND,int index,LONG_PTR& value){value=index==GWL_STYLE?style:ex;return true;}
 bool write(HWND,int index,LONG_PTR value){if(failure=="style-write")return false;if(index==GWL_STYLE)style=value;else ex=value;return true;}
 bool reparent(HWND,HWND p){if(failure=="reparent")return false;held_parent=p;return true;}
 bool position(HWND,int x,int y,int w,int h){if(failure=="position")return false;moved=true;rect={x,y,x+w,y+h};return true;}
 bool bounds(HWND,HWND,RECT& value){value=rect;if(failure=="bounds-readback")value.right++;return true;}
 void show(HWND,bool visible){if(visible)style|=WS_VISIBLE;else style&=~WS_VISIBLE;}
};
int main(){
 for(const auto& failure:std::vector<std::string>{"style-write","reparent","position","bounds-readback","parent-readback"}){
  FakeWindowOps ops;ops.failure=failure;auto error=attach(ops,reinterpret_cast<HWND>(1),reinterpret_cast<HWND>(2),7,10,50,600,400,true);
  if(error.empty()||(ops.style&WS_VISIBLE)!=0){std::cerr<<"failure escaped presentation transaction: "<<failure<<"\n";return 1;}
  std::cout<<failure<<": "<<error<<"; hidden, no successful presentation result\n";
 }
 FakeWindowOps ops;auto error=attach(ops,reinterpret_cast<HWND>(1),reinterpret_cast<HWND>(2),7,10,50,600,400,true);
 if(!error.empty()||(ops.style&WS_CHILD)==0||(ops.style&WS_VISIBLE)==0||ops.held_parent!=reinterpret_cast<HWND>(2)){std::cerr<<"positive verified transaction failed: "<<error<<"\n";return 1;}
 std::cout<<"verified parent/style/bounds transaction accepted\n";
}
