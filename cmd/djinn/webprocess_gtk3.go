//go:build linux && cgo && gtk3 && !headless

package main

/*
#cgo linux pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <stdint.h>
#include <stdlib.h>
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

extern void djinnWebProcessEnded(uintptr_t id, int reason);
extern void djinnLoadFailed(uintptr_t id, char *uri, char *message);
extern void djinnEvaluated(uintptr_t id, char *json, char *message);

static void djinn_web_process_terminated(WebKitWebView *view, WebKitWebProcessTerminationReason reason, gpointer id) {
	djinnWebProcessEnded((uintptr_t)id, (int)reason);
}

static gboolean djinn_load_failed(WebKitWebView *view, WebKitLoadEvent event, gchar *uri, GError *error, gpointer id) {
	djinnLoadFailed((uintptr_t)id, uri, error != NULL ? error->message : NULL);
	return FALSE; // WebKit's own handler runs too.
}

// djinn_find_webview is the WebKitWebView in widget, or NULL.
static GtkWidget *djinn_find_webview(GtkWidget *widget) {
	if (WEBKIT_IS_WEB_VIEW(widget)) {
		return widget;
	}
	if (!GTK_IS_CONTAINER(widget)) {
		return NULL;
	}
	GList *children = gtk_container_get_children(GTK_CONTAINER(widget));
	GtkWidget *found = NULL;
	for (GList *l = children; l != NULL && found == NULL; l = l->next) {
		found = djinn_find_webview(GTK_WIDGET(l->data));
	}
	g_list_free(children);
	return found;
}

// djinn_watch_view hears the end of the view's web process and its failed loads, and has WebKit write the page's
// console to the standard output.
static int djinn_watch_view(void *window, uintptr_t id) {
	GtkWidget *widget = djinn_find_webview(GTK_WIDGET(window));
	if (widget == NULL) {
		return 0;
	}
	WebKitWebView *view = WEBKIT_WEB_VIEW(widget);
	g_signal_connect(view, "web-process-terminated", G_CALLBACK(djinn_web_process_terminated), (gpointer)id);
	g_signal_connect(view, "load-failed", G_CALLBACK(djinn_load_failed), (gpointer)id);
	webkit_settings_set_enable_write_console_messages_to_stdout(webkit_web_view_get_settings(view), TRUE);
	return 1;
}

static void djinn_evaluated(GObject *view, GAsyncResult *result, gpointer id) {
	GError *error = NULL;
	JSCValue *value = webkit_web_view_evaluate_javascript_finish(WEBKIT_WEB_VIEW(view), result, &error);
	if (value == NULL) {
		djinnEvaluated((uintptr_t)id, NULL, error != NULL ? error->message : NULL);
		g_clear_error(&error);
		return;
	}
	char *json = jsc_value_to_json(value, 0);
	djinnEvaluated((uintptr_t)id, json, NULL);
	g_free(json);
	g_object_unref(value);
}

// djinn_evaluate evaluates script in the view of window, its answer to djinnEvaluated with id; 0 without a view.
static int djinn_evaluate(void *window, const char *script, uintptr_t id) {
	GtkWidget *widget = djinn_find_webview(GTK_WIDGET(window));
	if (widget == NULL) {
		return 0;
	}
	webkit_web_view_evaluate_javascript(WEBKIT_WEB_VIEW(widget), script, -1, NULL, NULL, NULL, djinn_evaluated, (gpointer)id);
	return 1;
}
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/empowill/djinn/internal/logx"
)

// views holds the hooks of each view watched, by the number given to its signals.
var views struct {
	sync.Mutex
	last  uintptr
	hooks map[uintptr]viewHooks
}

// watchView hears the view in native, a GtkWindow: the end of its web process, whenever it comes, and its failed
// loads; WebKit writes its page's console on djinn up's output. It runs on the main thread.
func watchView(native unsafe.Pointer, hooks viewHooks) {
	if native == nil {
		return
	}
	views.Lock()
	views.last++
	id := views.last
	if views.hooks == nil {
		views.hooks = map[uintptr]viewHooks{}
	}
	views.hooks[id] = hooks
	views.Unlock()
	if C.djinn_watch_view(native, C.uintptr_t(id)) == 0 {
		logx.L().Warn("window: no web view to watch")
	}
}

// viewOf is the hooks of the view numbered id.
func viewOf(id uintptr) viewHooks {
	views.Lock()
	defer views.Unlock()
	return views.hooks[id]
}

// webProcessReasons names the reasons of web-process-terminated, WebKitWebProcessTerminationReason, for the watch and
// for the log.
var webProcessReasons = map[int][2]string{
	0: {"crashed", "crashed"},
	1: {"memory", "exceeded its memory limit"},
	2: {"terminated", "terminated by API"},
}

// webProcessEnded is the signal web-process-terminated of WebKitGTK, for the view numbered id.
func webProcessEnded(id uintptr, reason int) {
	names, ok := webProcessReasons[reason]
	if !ok {
		names = [2]string{"crashed", "ended for an unknown reason"}
	}
	logx.L().Warn("window: the web process of the page ended", "reason", names[1], "code", reason)
	if ended := viewOf(id).ended; ended != nil {
		ended(names[0])
	}
}

// loadFailed is the signal load-failed of WebKitGTK, for the view numbered id.
func loadFailed(id uintptr, uri, message string) {
	if failed := viewOf(id).failed; failed != nil {
		failed(uri + ": " + message)
	}
}

// evaluated is one answer of the page to evaluateJS: its JSON, or the error.
type evaluated struct {
	json string
	err  error
}

// evaluations holds the evaluations waiting for their answer, by the number given to their callback.
var evaluations struct {
	sync.Mutex
	last    uintptr
	pending map[uintptr]chan evaluated
}

// evaluateJS evaluates script in the page of window, on the main thread, and gives its value as JSON once WebKit
// answers, or ctx's error: a page whose JS thread does not run never answers. Unlike Wails's ExecJS, it gets the
// value back (W181's probe of a silent page).
func evaluateJS(ctx context.Context, window *application.WebviewWindow, script string) (string, error) {
	answer := make(chan evaluated, 1)
	evaluations.Lock()
	evaluations.last++
	id := evaluations.last
	if evaluations.pending == nil {
		evaluations.pending = map[uintptr]chan evaluated{}
	}
	evaluations.pending[id] = answer
	evaluations.Unlock()
	defer func() {
		evaluations.Lock()
		delete(evaluations.pending, id)
		evaluations.Unlock()
	}()
	application.InvokeAsync(func() {
		native := window.NativeWindow()
		if native == nil {
			answer <- evaluated{err: errors.New("no native window")}
			return
		}
		text := C.CString(script)
		defer C.free(unsafe.Pointer(text))
		if C.djinn_evaluate(native, text, C.uintptr_t(id)) == 0 {
			answer <- evaluated{err: errors.New("no web view")}
		}
	})
	select {
	case a := <-answer:
		return a.json, a.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// jsEvaluated is the answer of webkit_web_view_evaluate_javascript, for the evaluation numbered id: dropped when it
// comes after its wait.
func jsEvaluated(id uintptr, json string, err error) {
	evaluations.Lock()
	answer := evaluations.pending[id]
	evaluations.Unlock()
	if answer == nil {
		return
	}
	select {
	case answer <- evaluated{json: json, err: err}:
	default:
	}
}
