// Narrow shared-pixel transport for CEF's official CPU OSR callbacks.
// flock is released by the kernel on process death; a killed writer cannot
// strand the host in a shared mutex. Native UI readers never block on a frame.
#pragma once
#include <cstdint>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>
namespace lab_osr {
constexpr int kMaxWidth = 4096, kMaxHeight = 3200;
struct Header {
  uint64_t sequence;
  uint32_t width, height;
};
constexpr size_t kBytes = sizeof(Header) + size_t(kMaxWidth) * kMaxHeight * 4;
struct Surface {
  int fd = -1;
  void *memory = nullptr;
  bool Open(const char *path, bool create) {
    fd = open(path, O_RDWR | O_NOFOLLOW | (create ? O_CREAT : 0), 0600);
    if (fd < 0)
      return false;
    if (create && ftruncate(fd, kBytes)) {
      Close();
      return false;
    }
    struct stat s{};
    if (fstat(fd, &s) || s.st_size != static_cast<off_t>(kBytes)) {
      Close();
      return false;
    }
    memory = mmap(nullptr, kBytes, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    if (memory == MAP_FAILED) {
      memory = nullptr;
      Close();
      return false;
    }
    if (create)
      *header() = Header{};
    return true;
  }
  Header *header() { return static_cast<Header *>(memory); }
  unsigned char *pixels() {
    return static_cast<unsigned char *>(memory) + sizeof(Header);
  }
  void Close() {
    if (memory)
      munmap(memory, kBytes);
    if (fd >= 0)
      close(fd);
    memory = nullptr;
    fd = -1;
  }
};
} // namespace lab_osr
