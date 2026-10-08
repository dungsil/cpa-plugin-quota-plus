// Command quota-plus builds the plugin as a C shared library
// (go build -buildmode=c-shared). The exported functions only convert between
// C buffers and Go bytes; every behavior lives in internal/handler.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static int quota_plus_host_call(const cliproxy_host_api* host, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (host == NULL || host->call == NULL) {
		return -1;
	}
	return host->call(host->host_ctx, method, request, request_len, response);
}

static void quota_plus_host_free(const cliproxy_host_api* host, void* ptr, size_t len) {
	if (host != NULL && host->free_buffer != NULL && ptr != NULL) {
		host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"unsafe"

	"github.com/dungsil/cpa-plugin-quota-plus/internal/handler"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// hostAPI is the host function table passed to cliproxy_plugin_init. The host
// owns it and keeps it alive while the plugin is loaded.
var hostAPI atomic.Pointer[C.cliproxy_host_api]

var rpc = handler.New(callHost, http.DefaultTransport)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPI.Store(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	if method == nil {
		raw, _ := pluginabi.NewErrorEnvelope("invalid_method", "method is required")
		writeResponse(response, raw)
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	writeResponse(response, rpc.Handle(C.GoString(method), requestBytes))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	hostAPI.Store(nil)
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if len(raw) == 0 {
		return
	}
	response.ptr = C.CBytes(raw)
	response.len = C.size_t(len(raw))
}

func callHost(method string, request []byte) ([]byte, error) {
	host := hostAPI.Load()
	if host == nil {
		return nil, errors.New("host callbacks are unavailable")
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var cRequest *C.uint8_t
	if len(request) > 0 {
		cRequest = (*C.uint8_t)(C.CBytes(request))
		defer C.free(unsafe.Pointer(cRequest))
	}
	var response C.cliproxy_buffer
	rc := C.quota_plus_host_call(host, cMethod, cRequest, C.size_t(len(request)), &response)
	var out []byte
	if response.ptr != nil {
		out = C.GoBytes(response.ptr, C.int(response.len))
		C.quota_plus_host_free(host, response.ptr, response.len)
	}
	if rc != 0 {
		return nil, fmt.Errorf("host call %s returned %d", method, int(rc))
	}
	return out, nil
}
