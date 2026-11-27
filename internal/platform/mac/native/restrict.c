// Worker and profile-maintenance process policies applied before DSH entry.
#define NAPI_VERSION 8
#include <node_api.h>
#include <sandbox.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <spawn.h>
#include <sys/wait.h>
#include <unistd.h>

static int install_policy(const char *profile) {
  char *error = NULL;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  int result = sandbox_init(profile, 0, &error);
  if (error) sandbox_free_error(error);
#pragma clang diagnostic pop
  return result;
}

static int allows_other_program(void) {
  pid_t spawned = 0;
  char *args[] = {"/usr/bin/true", NULL};
  char *environment[] = {NULL};
  int error = posix_spawn(&spawned, args[0], NULL, NULL, args, environment);
  if (error == 0) { while (waitpid(spawned, NULL, 0) < 0 && errno == EINTR) {} }
  return error != EPERM;
}

static napi_value apply(napi_env env, napi_callback_info info) {
  (void)info;
  if (install_policy("(version 1)(allow default)(deny process-fork)(deny process-exec)") != 0) {
    napi_throw_error(env, "MAC_PREVIEW_SANDBOX_REQUIRED", "Mac preview process restrictions could not be applied.");
    return NULL;
  }
  // Verify actual kernel operations, not just a policy parser return value.
  errno = 0;
  pid_t child = fork();
  int fork_error = errno;
  if (child == 0) _exit(126);
  if (child > 0) { while (waitpid(child, NULL, 0) < 0 && errno == EINTR) {} }
  if (child != -1 || fork_error != EPERM || allows_other_program()) {
    napi_throw_error(env, "MAC_PREVIEW_SANDBOX_REQUIRED", "Mac preview kernel process restrictions could not be verified.");
    return NULL;
  }
  napi_value value;
  napi_get_boolean(env, true, &value);
  return value;
}

static napi_value apply_package_maintenance(napi_env env, napi_callback_info info) {
  size_t argc = 1, length = 0;
  napi_value argument;
  char path[PATH_MAX];
  char escaped[2 * PATH_MAX];
  char profile[sizeof(escaped) + 128];
  if (napi_get_cb_info(env, info, &argc, &argument, NULL, NULL) != napi_ok || argc != 1 ||
      napi_get_value_string_utf8(env, argument, NULL, 0, &length) != napi_ok ||
      length == 0 || length >= sizeof(path) ||
      napi_get_value_string_utf8(env, argument, path, sizeof(path), NULL) != napi_ok ||
      path[0] != '/') {
    napi_throw_error(env, "MAC_PROFILE_MAINTENANCE_SANDBOX_REQUIRED", "A selected Node path is required.");
    return NULL;
  }
  size_t offset = 0;
  for (size_t i = 0; i < length; ++i) {
    if ((unsigned char)path[i] < 32) {
      napi_throw_error(env, "MAC_PROFILE_MAINTENANCE_SANDBOX_REQUIRED", "The selected Node path is invalid.");
      return NULL;
    }
    if (path[i] == '"' || path[i] == '\\') escaped[offset++] = '\\';
    escaped[offset++] = path[i];
  }
  escaped[offset] = '\0';
  snprintf(profile, sizeof(profile),
      "(version 1)(allow default)(deny process-exec)(allow process-exec (literal \"%s\"))", escaped);
  if (install_policy(profile) != 0 || allows_other_program()) {
    napi_throw_error(env, "MAC_PROFILE_MAINTENANCE_SANDBOX_REQUIRED", "Mac package process restrictions could not be applied.");
    return NULL;
  }
  napi_value value;
  napi_get_boolean(env, true, &value);
  return value;
}

static napi_value verify_package_maintenance(napi_env env, napi_callback_info info) {
  (void)info;
  napi_value value;
  napi_get_boolean(env, !allows_other_program(), &value);
  return value;
}

static napi_value initialize(napi_env env, napi_value exports) {
  napi_value function;
  if (napi_create_function(env, "apply", NAPI_AUTO_LENGTH, apply, NULL, &function) != napi_ok ||
      napi_set_named_property(env, exports, "apply", function) != napi_ok ||
      napi_create_function(env, "applyPackageMaintenance", NAPI_AUTO_LENGTH, apply_package_maintenance, NULL, &function) != napi_ok ||
      napi_set_named_property(env, exports, "applyPackageMaintenance", function) != napi_ok ||
      napi_create_function(env, "verifyPackageMaintenance", NAPI_AUTO_LENGTH, verify_package_maintenance, NULL, &function) != napi_ok ||
      napi_set_named_property(env, exports, "verifyPackageMaintenance", function) != napi_ok) return NULL;
  return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME, initialize)
