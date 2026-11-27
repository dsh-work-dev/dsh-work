#pragma once
// Windows' maintained Common Item Dialog owns selection, filtering and shell
// navigation. A separate STA keeps CEF responsive to document/view revocation.
#include <shobjidl.h>
#include <atomic>
#include <memory>
#include <thread>
#include <functional>
namespace dsh_file_picker {
struct Request {
  std::atomic_bool canceled{false};
  std::atomic_bool cancel_posted{false};
  std::atomic_int phase{0};
  std::atomic_long cancel_result{E_PENDING},window_result{E_PENDING},show_result{E_PENDING};
  std::atomic<HWND> picker_window{nullptr};
};
struct Owner {
  HWND root, parent, guest;
  DWORD pid;
  uint64_t started;
};
inline bool SameOwner(const Owner& owner,HANDLE process){
  FILETIME created,exited,kernel,user;DWORD pid=0,guest_pid=0;
  return process&&WaitForSingleObject(process,0)==WAIT_TIMEOUT&&GetProcessTimes(process,&created,&exited,&kernel,&user)&&
    ((static_cast<uint64_t>(created.dwHighDateTime)<<32)|created.dwLowDateTime)==owner.started&&
    GetWindowThreadProcessId(owner.root,&pid)&&pid==owner.pid&&
    GetWindowThreadProcessId(owner.parent,&pid)&&pid==owner.pid&&
    GetWindowThreadProcessId(owner.guest,&guest_pid)&&guest_pid==GetCurrentProcessId()&&
    GetParent(owner.guest)==owner.parent&&GetAncestor(owner.guest,GA_ROOT)==owner.root&&IsWindowVisible(owner.root);
}
inline LRESULT CALLBACK WindowProc(HWND window, UINT message, WPARAM w, LPARAM l) {
  if(message==WM_CREATE){SetWindowLongPtrW(window,GWLP_USERDATA,reinterpret_cast<LONG_PTR>(reinterpret_cast<CREATESTRUCTW*>(l)->lpCreateParams));return 0;}
  if(message==WM_TIMER){
    auto pair=reinterpret_cast<std::pair<Request*,IFileOpenDialog*>*>(GetWindowLongPtrW(window,GWLP_USERDATA));
    if(pair&&!pair->first->picker_window.load()){
      IOleWindow* modal=nullptr;HWND picker=nullptr;
      auto result=pair->second->QueryInterface(IID_PPV_ARGS(&modal));
      if(SUCCEEDED(result)){result=modal->GetWindow(&picker);modal->Release();}
      pair->first->window_result=result;
      pair->first->picker_window=picker;
    }
    if(pair&&pair->first->canceled.load()&&!pair->first->cancel_posted.load()){
      auto picker=pair->first->picker_window.load();DWORD pid=0;
      if(picker&&GetWindowThreadProcessId(picker,&pid)==GetCurrentThreadId()&&pid==GetCurrentProcessId()){
        // Match Chromium's AutoCloseDialogEventHandler: IFileDialog::Close
        // expects an IFileDialogEvents callback, not a window/timer callback.
        // https://github.com/chromium/chromium/blob/156.0.8078.80/ui/shell_dialogs/auto_close_dialog_event_handler_win.cc#L86-L88
        pair->first->phase=2;
        bool posted=!!PostMessageW(picker,WM_CLOSE,0,0);
        pair->first->cancel_result=posted?S_OK:HRESULT_FROM_WIN32(GetLastError());
        pair->first->cancel_posted=posted;
        pair->first->phase=3;
      }
    }
    return 0;
  }
  return DefWindowProcW(window,message,w,l);
}
inline std::shared_ptr<Request> Open(Owner owner, bool multiple, bool folder,
    std::wstring title, std::vector<std::wstring> extensions,
    std::function<void(std::vector<CefString>)> complete) {
  auto request=std::make_shared<Request>();
  std::thread([request,owner,multiple,folder,title=std::move(title),extensions=std::move(extensions),complete=std::move(complete)]() mutable {
    std::vector<CefString> files;
    auto process=OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE,FALSE,owner.pid);
    const auto initialized=CoInitializeEx(nullptr,COINIT_APARTMENTTHREADED|COINIT_DISABLE_OLE1DDE);
    IFileOpenDialog* dialog=nullptr;
    if(SUCCEEDED(initialized)&&SUCCEEDED(CoCreateInstance(CLSID_FileOpenDialog,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&dialog)))){
      DWORD options=0;dialog->GetOptions(&options);
      options|=FOS_FORCEFILESYSTEM|FOS_PATHMUSTEXIST|FOS_FILEMUSTEXIST|FOS_NOCHANGEDIR|FOS_DONTADDTORECENT;
      if(multiple)options|=FOS_ALLOWMULTISELECT;
      if(folder)options|=FOS_PICKFOLDERS;
      dialog->SetOptions(options);dialog->SetTitle(title.c_str());
      std::wstring filter;
      for(const auto& ext:extensions){
        // CEF's MIME expansion supplies extensions, never shell paths. Ignore
        // malformed filters rather than interpreting page text as a path.
        if(ext.empty()||ext.size()>512||ext.find_first_not_of(L".abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789;_+-")!=std::wstring::npos)continue;
        size_t start=0;while(start<ext.size()){auto end=ext.find(L';',start);auto part=ext.substr(start,end-start);if(part.size()>1&&part.front()==L'.'){if(!filter.empty())filter+=L';';filter+=L'*'+part;}if(end==std::wstring::npos)break;start=end+1;}
      }
      COMDLG_FILTERSPEC filters[]={{L"Selected file types",filter.c_str()},{L"All files",L"*.*"}};
      if(!filter.empty()&&!folder)dialog->SetFileTypes(2,filters);
      WNDCLASSW type{};type.lpfnWndProc=WindowProc;type.hInstance=GetModuleHandleW(nullptr);type.lpszClassName=L"DSH.FilePicker.Cancel";
      RegisterClassW(&type);
      std::pair<Request*,IFileOpenDialog*> pair{request.get(),dialog};
      auto window=CreateWindowExW(0,type.lpszClassName,L"",0,0,0,0,0,HWND_MESSAGE,nullptr,type.hInstance,&pair);
      bool ready=window&&SetTimer(window,1,50,nullptr)&&SameOwner(owner,process);
      request->phase=1;
      HRESULT shown=E_ABORT;
      if(ready&&!request->canceled.load()){
        shown=dialog->Show(owner.root);request->show_result=shown;request->phase=4;
      }
      if(SUCCEEDED(shown)&&!request->canceled.load()&&SameOwner(owner,process)){
        IShellItemArray* result=nullptr;
        if(SUCCEEDED(dialog->GetResults(&result))){
          DWORD count=0;result->GetCount(&count);
          for(DWORD i=0;i<count;++i){IShellItem* item=nullptr;PWSTR path=nullptr;
            if(SUCCEEDED(result->GetItemAt(i,&item))){if(SUCCEEDED(item->GetDisplayName(SIGDN_FILESYSPATH,&path))){files.emplace_back(path);CoTaskMemFree(path);}item->Release();}
          }
          result->Release();
        }
      }
      if(window){KillTimer(window,1);DestroyWindow(window);}dialog->Release();
      request->picker_window=nullptr;
      request->phase=5;
    }
    if(SUCCEEDED(initialized))CoUninitialize();
    request->phase=6;
    if(process)CloseHandle(process);
    CefPostTask(TID_UI,base::BindOnce([](std::function<void(std::vector<CefString>)> callback,std::vector<CefString> selected){callback(std::move(selected));},std::move(complete),std::move(files)));
  }).detach();
  return request;
}
}
