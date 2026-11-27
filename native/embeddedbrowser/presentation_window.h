#pragma once
#include <windows.h>
#include <string>

namespace dsh_presenter {
// This narrow Win32 face permits deterministic failure checks without another
// UI host. Production always uses the real operations below.
struct WindowOps {
 bool valid(HWND w){return IsWindow(w)!=FALSE;}
 HWND parent(HWND w){return GetParent(w);}
 HWND root(HWND w){return GetAncestor(w,GA_ROOT);}
 DWORD pid(HWND w){DWORD p=0;GetWindowThreadProcessId(w,&p);return p;}
 bool read(HWND w,int index,LONG_PTR& value){SetLastError(0);value=GetWindowLongPtr(w,index);return value!=0||GetLastError()==0;}
 bool write(HWND w,int index,LONG_PTR value){SetLastError(0);auto previous=SetWindowLongPtr(w,index,value);return previous!=0||GetLastError()==0;}
 bool reparent(HWND w,HWND p){SetLastError(0);auto previous=SetParent(w,p);return previous!=nullptr||GetLastError()==0;}
 bool position(HWND w,int x,int y,int width,int height){return SetWindowPos(w,nullptr,x,y,width,height,SWP_NOZORDER|SWP_NOACTIVATE|SWP_FRAMECHANGED)!=FALSE;}
 bool bounds(HWND w,HWND p,RECT& value){if(!GetWindowRect(w,&value))return false;SetLastError(0);auto result=MapWindowPoints(nullptr,p,reinterpret_cast<POINT*>(&value),2);return result!=0||GetLastError()==0;}
 void show(HWND w,bool visible){ShowWindow(w,visible?SW_SHOWNOACTIVATE:SW_HIDE);}
};

template<class Ops> std::string hide(Ops& ops,HWND window){
 if(!ops.valid(window))return "CEF_PRESENT_WINDOW_GONE";
 ops.show(window,false);LONG_PTR style=0;
 if(!ops.read(window,GWL_STYLE,style)||(style&WS_VISIBLE)!=0)return "CEF_PRESENT_HIDE_FAILED";
 return {};
}

template<class Ops> std::string attach(Ops& ops,HWND window,HWND parent,DWORD pid,int x,int y,int width,int height,bool visible){
 auto fail=[&](const char* code){auto hidden=hide(ops,window);return std::string(code)+(hidden.empty()?"":" / "+hidden);};
 if(auto error=hide(ops,window);!error.empty())return error;
 if(!ops.valid(parent)||ops.pid(parent)!=pid||ops.root(parent)!=parent)return fail("CEF_PRESENT_PARENT_REJECTED");
 LONG_PTR style=0,ex=0;
 if(!ops.read(window,GWL_STYLE,style)||!ops.read(window,GWL_EXSTYLE,ex))return fail("CEF_PRESENT_STYLE_READ_FAILED");
 const LONG_PTR frame=WS_POPUP|WS_CAPTION|WS_THICKFRAME|WS_SYSMENU|WS_MINIMIZEBOX|WS_MAXIMIZEBOX|WS_BORDER|WS_DLGFRAME;
 const LONG_PTR ex_frame=WS_EX_NOACTIVATE|WS_EX_WINDOWEDGE|WS_EX_CLIENTEDGE|WS_EX_DLGMODALFRAME;
 const LONG_PTR desired=(style&~frame)|WS_CHILD|WS_CLIPCHILDREN|WS_CLIPSIBLINGS;
 if(!ops.write(window,GWL_STYLE,desired)||!ops.write(window,GWL_EXSTYLE,ex&~ex_frame))return fail("CEF_PRESENT_STYLE_WRITE_FAILED");
 if(ops.parent(window)!=parent&&!ops.reparent(window,parent))return fail("CEF_PRESENT_REPARENT_FAILED");
 if(!ops.position(window,x,y,width,height))return fail("CEF_PRESENT_POSITION_FAILED");
 RECT actual{};
 auto verify=[&](){return ops.valid(parent)&&ops.valid(window)&&ops.pid(parent)==pid&&ops.root(parent)==parent&&ops.parent(window)==parent&&ops.read(window,GWL_STYLE,style)&&ops.read(window,GWL_EXSTYLE,ex)&&(style&(frame|WS_CHILD))==WS_CHILD&&(ex&ex_frame)==0&&ops.bounds(window,parent,actual)&&actual.left==x&&actual.top==y&&actual.right==x+width&&actual.bottom==y+height;};
 if(!verify())return fail("CEF_PRESENT_READBACK_FAILED");
 ops.show(window,visible);
 if(!verify()||((style&WS_VISIBLE)!=0)!=visible)return fail("CEF_PRESENT_VISIBILITY_VERIFY_FAILED");
 return {};
}
} // namespace dsh_presenter
