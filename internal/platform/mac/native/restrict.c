// Fixed Node-only preview. Applied before importing any DSH or profile module.
#define NAPI_VERSION 8
#include <node_api.h>
#include <sandbox.h>
#include <errno.h>
#include <stdlib.h>
#include <spawn.h>
#include <sys/wait.h>
#include <unistd.h>

static napi_value apply(napi_env env, napi_callback_info info) {
  (void)info;
  char *error = NULL;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  int result = sandbox_init("(version 1)(allow default)(deny process-fork)(deny process-exec)", 0, &error);
  if (result != 0) {
    if (error) sandbox_free_error(error);
    napi_throw_error(env, "MAC_PREVIEW_SANDBOX_REQUIRED", "Mac preview process restrictions could not be applied.");
    return NULL;
  }
#pragma clang diagnostic pop
  // Verify actual kernel operations, not just a policy parser return value.
  errno = 0;
  pid_t child = fork();
  int fork_error = errno;
  if (child == 0) _exit(126);
  if (child > 0) { while (waitpid(child, NULL, 0) < 0 && errno == EINTR) {} }
  pid_t spawned = 0;
  char *args[] = {"/usr/bin/true", NULL};
  char *environment[] = {NULL};
  int spawn_error = posix_spawn(&spawned, args[0], NULL, NULL, args, environment);
  if (spawn_error == 0) { while (waitpid(spawned, NULL, 0) < 0 && errno == EINTR) {} }
  if (child != -1 || fork_error != EPERM || spawn_error != EPERM) {
    napi_throw_error(env, "MAC_PREVIEW_SANDBOX_REQUIRED", "Mac preview kernel process restrictions could not be verified.");
    return NULL;
  }
  napi_value value;
  napi_get_boolean(env, true, &value);
  return value;
}
static napi_value initialize(napi_env env, napi_value exports) {
  napi_value function;
  if (napi_create_function(env, "apply", NAPI_AUTO_LENGTH, apply, NULL, &function) != napi_ok ||
      napi_set_named_property(env, exports, "apply", function) != napi_ok) return NULL;
  return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME, initialize)
