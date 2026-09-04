#include "pulse_recorder.h"

#include <pulse/error.h>
#include <pulse/simple.h>

#include <atomic>
#include <cstdint>
#include <string>
#include <thread>
#include <vector>

namespace {

constexpr char kChannelName[] = "dev.fundus.app/recorder";
// 100 ms of S16LE at 16 kHz mono.
constexpr size_t kChunkBytes = 3200;

FlMethodChannel* g_channel = nullptr;

// One recording. Owned by its reader thread once started: `stop` only clears
// the running flag, the thread frees the stream and the session itself, so
// the UI thread never blocks on the audio stack.
struct Session {
  pa_simple* stream = nullptr;
  std::atomic<bool> running{false};
};

Session* g_session = nullptr;

struct Delivery {
  Session* session;
  bool error;
  std::string message;
  std::vector<uint8_t> data;
};

// Runs on the GTK main thread: hands a chunk (or an error) to Dart, unless
// the session was stopped meanwhile.
gboolean deliver(gpointer user_data) {
  auto* d = static_cast<Delivery*>(user_data);
  if (g_channel != nullptr && d->session == g_session) {
    if (d->error) {
      g_autoptr(FlValue) v = fl_value_new_string(d->message.c_str());
      fl_method_channel_invoke_method(g_channel, "error", v, nullptr, nullptr,
                                      nullptr);
    } else {
      g_autoptr(FlValue) v =
          fl_value_new_uint8_list(d->data.data(), d->data.size());
      fl_method_channel_invoke_method(g_channel, "chunk", v, nullptr, nullptr,
                                      nullptr);
    }
  }
  delete d;
  return G_SOURCE_REMOVE;
}

void read_loop(Session* session) {
  std::vector<uint8_t> buf(kChunkBytes);
  int err = 0;
  while (session->running.load()) {
    if (pa_simple_read(session->stream, buf.data(), buf.size(), &err) < 0) {
      if (session->running.load()) {
        g_idle_add(deliver, new Delivery{session, true, pa_strerror(err), {}});
      }
      break;
    }
    g_idle_add(deliver, new Delivery{session, false, "", buf});
  }
  pa_simple_free(session->stream);
  delete session;
}

FlMethodResponse* start() {
  if (g_session != nullptr) {
    return FL_METHOD_RESPONSE(fl_method_success_response_new(nullptr));
  }
  pa_sample_spec spec{};
  spec.format = PA_SAMPLE_S16LE;
  spec.rate = 16000;
  spec.channels = 1;
  pa_buffer_attr attr{};
  attr.maxlength = static_cast<uint32_t>(-1);
  attr.tlength = static_cast<uint32_t>(-1);
  attr.prebuf = static_cast<uint32_t>(-1);
  attr.minreq = static_cast<uint32_t>(-1);
  attr.fragsize = kChunkBytes;
  int err = 0;
  pa_simple* stream =
      pa_simple_new(nullptr, "Fundus", PA_STREAM_RECORD, nullptr, "Dictation",
                    &spec, nullptr, &attr, &err);
  if (stream == nullptr) {
    return FL_METHOD_RESPONSE(
        fl_method_error_response_new("unavailable", pa_strerror(err), nullptr));
  }
  auto* session = new Session;
  session->stream = stream;
  session->running = true;
  g_session = session;
  std::thread(read_loop, session).detach();
  return FL_METHOD_RESPONSE(fl_method_success_response_new(nullptr));
}

FlMethodResponse* stop() {
  if (g_session != nullptr) {
    g_session->running = false;
    g_session = nullptr;
  }
  return FL_METHOD_RESPONSE(fl_method_success_response_new(nullptr));
}

void method_call_cb(FlMethodChannel* channel, FlMethodCall* method_call,
                    gpointer user_data) {
  const gchar* method = fl_method_call_get_name(method_call);
  g_autoptr(FlMethodResponse) response = nullptr;
  if (g_strcmp0(method, "start") == 0) {
    response = start();
  } else if (g_strcmp0(method, "stop") == 0) {
    response = stop();
  } else {
    response = FL_METHOD_RESPONSE(fl_method_not_implemented_response_new());
  }
  g_autoptr(GError) error = nullptr;
  if (!fl_method_call_respond(method_call, response, &error)) {
    g_warning("recorder: failed to respond: %s", error->message);
  }
}

}  // namespace

void pulse_recorder_register(FlBinaryMessenger* messenger) {
  g_autoptr(FlStandardMethodCodec) codec = fl_standard_method_codec_new();
  g_channel =
      fl_method_channel_new(messenger, kChannelName, FL_METHOD_CODEC(codec));
  fl_method_channel_set_method_call_handler(g_channel, method_call_cb, nullptr,
                                            nullptr);
}
