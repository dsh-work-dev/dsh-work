cmake_minimum_required(VERSION 3.21)
project(dsh_cef_owner_probe LANGUAGES CXX OBJCXX)
if(NOT APPLE)
  message(FATAL_ERROR "This diagnostic requires macOS")
endif()
list(APPEND CMAKE_MODULE_PATH "${CEF_ROOT}/cmake")
find_package(CEF REQUIRED)
add_library(probe_official_wrapper STATIC IMPORTED)
set_target_properties(probe_official_wrapper PROPERTIES IMPORTED_LOCATION "${CEF_WRAPPER_LIBRARY}")
foreach(target cef_owner_broker cef_owner_helper cef_owner_inspect)
  if(target STREQUAL "cef_owner_broker")
    set(source broker.mm)
  elseif(target STREQUAL "cef_owner_helper")
    set(source helper.cc)
  else()
    set(source inspect.cc)
  endif()
  add_executable(${target} ${source})
  SET_EXECUTABLE_TARGET_PROPERTIES(${target})
  target_include_directories(${target} PRIVATE "${CEF_ROOT}")
  target_link_libraries(${target} probe_official_wrapper ${CEF_STANDARD_LIBS})
  set_target_properties(${target} PROPERTIES RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out")
endforeach()

# CEF's executable macro supplies CXX flags; broker.mm is a distinct OBJCXX
# language target and must request the same C++20 concepts explicitly.
set_target_properties(cef_owner_broker PROPERTIES
  OBJCXX_STANDARD 20
  OBJCXX_STANDARD_REQUIRED YES
  OBJCXX_EXTENSIONS NO
)
