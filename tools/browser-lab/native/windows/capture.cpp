// Single HWND Windows Graphics Capture. No picker, input, or monitor capture.
#include <windows.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wincodec.h>
#include <windows.graphics.capture.interop.h>
#include <windows.graphics.directx.direct3d11.interop.h>
#include <winrt/base.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Graphics.Capture.h>
#include <winrt/Windows.Graphics.DirectX.h>
#include <winrt/Windows.Graphics.DirectX.Direct3D11.h>
#include <cstdio>
#include <cwchar>
#include <mutex>
#include <memory>
#include <stdexcept>

using namespace winrt;
using namespace winrt::Windows::Graphics::Capture;
using namespace winrt::Windows::Graphics::DirectX;
using namespace winrt::Windows::Graphics::DirectX::Direct3D11;

void SavePng(ID3D11Device* device, ID3D11DeviceContext* context,
             ID3D11Texture2D* texture, UINT width, UINT height, const wchar_t* path) {
  D3D11_TEXTURE2D_DESC desc{};
  texture->GetDesc(&desc);
  if (width == 0 || height == 0 || width > desc.Width || height > desc.Height)
    throw std::runtime_error("Invalid captured content size");
  desc.Usage = D3D11_USAGE_STAGING;
  desc.BindFlags = 0;
  desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
  desc.MiscFlags = 0;
  com_ptr<ID3D11Texture2D> staging;
  check_hresult(device->CreateTexture2D(&desc, nullptr, staging.put()));
  context->CopyResource(staging.get(), texture);
  D3D11_MAPPED_SUBRESOURCE mapped{};
  check_hresult(context->Map(staging.get(), 0, D3D11_MAP_READ, 0, &mapped));
  try {
    com_ptr<IWICImagingFactory> factory;
    check_hresult(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER,
                                  __uuidof(IWICImagingFactory), factory.put_void()));
    com_ptr<IWICStream> stream;
    check_hresult(factory->CreateStream(stream.put()));
    check_hresult(stream->InitializeFromFilename(path, GENERIC_WRITE));
    com_ptr<IWICBitmapEncoder> encoder;
    check_hresult(factory->CreateEncoder(GUID_ContainerFormatPng, nullptr, encoder.put()));
    check_hresult(encoder->Initialize(stream.get(), WICBitmapEncoderNoCache));
    com_ptr<IWICBitmapFrameEncode> frame;
    check_hresult(encoder->CreateNewFrame(frame.put(), nullptr));
    check_hresult(frame->Initialize(nullptr));
    check_hresult(frame->SetSize(width, height));
    WICPixelFormatGUID format = GUID_WICPixelFormat32bppBGRA;
    check_hresult(frame->SetPixelFormat(&format));
    if (format != GUID_WICPixelFormat32bppBGRA)
      throw std::runtime_error("PNG encoder did not accept BGRA");
    check_hresult(frame->WritePixels(height, mapped.RowPitch, mapped.RowPitch * height,
                                    static_cast<BYTE*>(mapped.pData)));
    check_hresult(frame->Commit());
    check_hresult(encoder->Commit());
  } catch (...) {
    context->Unmap(staging.get(), 0);
    throw;
  }
  context->Unmap(staging.get(), 0);
}

int wmain(int argc, wchar_t** argv) {
  const wchar_t* phase = L"argument validation";
  if (argc != 3) {
    std::fwprintf(stderr, L"Usage: capture.exe <decimal-HWND> <output.png>\n");
    return 2;
  }
  try {
    wchar_t* end = nullptr;
    const auto value = _wcstoui64(argv[1], &end, 10);
    if (!value || !end || *end || argv[1][0] == L'-')
      throw std::runtime_error("Expected a nonzero decimal HWND");
    HWND hwnd = reinterpret_cast<HWND>(static_cast<uintptr_t>(value));
    if (!IsWindow(hwnd) || GetAncestor(hwnd, GA_ROOT) != hwnd)
      throw std::runtime_error("HWND must identify an existing top-level window");
    if (IsIconic(hwnd))
      throw std::runtime_error("Minimized windows cannot provide a capture frame");
    phase = L"WinRT initialization";
    init_apartment(apartment_type::multi_threaded);
    if (!GraphicsCaptureSession::IsSupported())
      throw std::runtime_error("Windows Graphics Capture is not supported");
    phase = L"CreateForWindow";
    auto interop = get_activation_factory<GraphicsCaptureItem, IGraphicsCaptureItemInterop>();
    GraphicsCaptureItem item{nullptr};
    check_hresult(interop->CreateForWindow(hwnd, guid_of<GraphicsCaptureItem>(), put_abi(item)));

    phase = L"D3D device creation";
    com_ptr<ID3D11Device> device;
    com_ptr<ID3D11DeviceContext> context;
    check_hresult(D3D11CreateDevice(nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr,
        D3D11_CREATE_DEVICE_BGRA_SUPPORT, nullptr, 0, D3D11_SDK_VERSION,
        device.put(), nullptr, context.put()));
    auto dxgi = device.as<IDXGIDevice>();
    com_ptr<IInspectable> inspectable;
    check_hresult(CreateDirect3D11DeviceFromDXGIDevice(dxgi.get(), inspectable.put()));
    auto rtDevice = inspectable.as<IDirect3DDevice>();
    auto size = item.Size();
    phase = L"capture session creation";
    auto pool = Direct3D11CaptureFramePool::CreateFreeThreaded(rtDevice,
        DirectXPixelFormat::B8G8R8A8UIntNormalized, 2, size);
    auto session = pool.CreateCaptureSession(item);
    session.IsCursorCaptureEnabled(false);
    struct State {
      std::mutex mutex;
      Direct3D11CaptureFrame frame{nullptr};
      hresult error{S_OK};
      handle ready{CreateEventW(nullptr, TRUE, FALSE, nullptr)};
    };
    auto state = std::make_shared<State>();
    if (!state->ready) throw_last_error();
    const auto token = pool.FrameArrived([state](auto const& sender, auto const&) {
      std::lock_guard<std::mutex> guard(state->mutex);
      if (state->frame || FAILED(state->error)) return;
      try {
        auto frame = sender.TryGetNextFrame();
        if (frame) {
          state->frame = frame;
          SetEvent(state->ready.get());
        }
      } catch (...) {
        state->error = to_hresult();
        SetEvent(state->ready.get());
      }
    });
    phase = L"StartCapture";
    session.StartCapture();
    const auto wait = WaitForSingleObject(state->ready.get(), 10000);
    pool.FrameArrived(token);
    if (wait != WAIT_OBJECT_0) {
      session.Close(); pool.Close();
      throw std::runtime_error("No capture frame arrived within 10 seconds");
    }
    Direct3D11CaptureFrame frame{nullptr};
    {
      std::lock_guard<std::mutex> guard(state->mutex);
      check_hresult(state->error);
      frame = state->frame;
    }
    auto access = frame.Surface().as<::Windows::Graphics::DirectX::Direct3D11::IDirect3DDxgiInterfaceAccess>();
    com_ptr<ID3D11Texture2D> texture;
    check_hresult(access->GetInterface(__uuidof(ID3D11Texture2D), texture.put_void()));
    const auto content = frame.ContentSize();
    phase = L"PNG encoding";
    SavePng(device.get(), context.get(), texture.get(), content.Width, content.Height, argv[2]);
    frame.Close();
    session.Close();
    pool.Close();
    std::wprintf(L"Captured HWND %llu: %d x %d -> %s\n", value, content.Width, content.Height, argv[2]);
    return 0;
  } catch (hresult_error const& e) {
    std::fwprintf(stderr, L"Capture failed at %s, 0x%08X: %s\n", phase, unsigned(e.code().value), e.message().c_str());
  } catch (std::exception const& e) {
    std::fprintf(stderr, "Capture failed: %s\n", e.what());
  }
  return 1;
}

