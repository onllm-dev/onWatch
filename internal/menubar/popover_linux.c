//go:build menubar && linux && cgo

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <gdk/gdk.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    GtkWidget *window;
    WebKitWebView *webview;
    int width;
    int height;
    gboolean visible;
} OnWatchPopover;

// Run a callback synchronously on the GTK main thread.
// fyne.io/systray already runs gtk_main(), so we use g_idle_add
// to dispatch into that loop - same pattern as macOS dispatch_sync.
typedef struct {
    void (*func)(void *data);
    void *data;
    GMutex mutex;
    GCond cond;
    gboolean done;
} MainThreadCall;

static gboolean main_thread_dispatch(gpointer user_data) {
    MainThreadCall *call = (MainThreadCall *)user_data;
    call->func(call->data);
    g_mutex_lock(&call->mutex);
    call->done = TRUE;
    g_cond_signal(&call->cond);
    g_mutex_unlock(&call->mutex);
    return G_SOURCE_REMOVE;
}

static void onwatch_run_on_main_sync(void (*func)(void *), void *data) {
    // Fast path: already on the main thread.
    if (g_main_context_is_owner(g_main_context_default())) {
        func(data);
        return;
    }

    MainThreadCall call;
    call.func = func;
    call.data = data;
    call.done = FALSE;
    g_mutex_init(&call.mutex);
    g_cond_init(&call.cond);

    g_idle_add(main_thread_dispatch, &call);

    // Wait for the main loop to process our idle callback.
    // Use a timed wait with context iteration fallback to avoid deadlock
    // if the GTK main loop has not started processing idle sources yet
    // (e.g. during early startup before gtk_main is fully entered).
    g_mutex_lock(&call.mutex);
    while (!call.done) {
        if (!g_cond_wait_until(&call.cond, &call.mutex,
                g_get_monotonic_time() + 50 * G_TIME_SPAN_MILLISECOND)) {
            // Timed out - pump the main context manually in case the
            // main loop is not yet iterating.
            g_mutex_unlock(&call.mutex);
            g_main_context_iteration(g_main_context_default(), FALSE);
            g_mutex_lock(&call.mutex);
        }
    }
    g_mutex_unlock(&call.mutex);
    g_mutex_clear(&call.mutex);
    g_cond_clear(&call.cond);
}

// focus-out-event handler: dismiss popover when it loses focus.
static gboolean on_focus_out(GtkWidget *widget, GdkEventFocus *event, gpointer user_data) {
    OnWatchPopover *popover = (OnWatchPopover *)user_data;
    if (popover->visible) {
        gtk_widget_hide(popover->window);
        popover->visible = FALSE;
    }
    return FALSE;
}

// Intercept navigation: allow localhost, open external URLs in browser.
static gboolean on_decide_policy(WebKitWebView *web_view,
                                  WebKitPolicyDecision *decision,
                                  WebKitPolicyDecisionType type,
                                  gpointer user_data) {
    if (type != WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
        return FALSE;
    }

    WebKitNavigationPolicyDecision *nav_decision = WEBKIT_NAVIGATION_POLICY_DECISION(decision);
    WebKitNavigationAction *action = webkit_navigation_policy_decision_get_navigation_action(nav_decision);
    WebKitURIRequest *request = webkit_navigation_action_get_request(action);
    const gchar *uri = webkit_uri_request_get_uri(request);

    if (uri == NULL) {
        webkit_policy_decision_ignore(decision);
        return TRUE;
    }

    // Allow about: and localhost URLs
    if (g_str_has_prefix(uri, "about:") ||
        g_str_has_prefix(uri, "http://localhost") ||
        g_str_has_prefix(uri, "http://127.0.0.1")) {
        webkit_policy_decision_use(decision);
        return TRUE;
    }

    // Open external URLs in default browser
    GError *error = NULL;
    // Use gtk_show_uri_on_window for GTK3
    gtk_show_uri_on_window(NULL, uri, GDK_CURRENT_TIME, &error);
    if (error) {
        g_error_free(error);
    }
    webkit_policy_decision_ignore(decision);
    return TRUE;
}

// Handle script messages from the frontend (onwatchResize, onwatchAction).
static void on_script_message(WebKitUserContentManager *manager,
                               WebKitJavascriptResult *js_result,
                               gpointer user_data) {
    // Script message handling - the frontend sends resize/action messages
    // via window.webkit.messageHandlers which are handled by the Go layer.
    (void)manager;
    (void)js_result;
    (void)user_data;
}

typedef struct {
    OnWatchPopover **result;
    int width;
    int height;
} CreateArgs;

static void do_create(void *data) {
    CreateArgs *args = (CreateArgs *)data;

    OnWatchPopover *popover = (OnWatchPopover *)calloc(1, sizeof(OnWatchPopover));
    if (!popover) {
        *args->result = NULL;
        return;
    }

    popover->width = args->width;
    popover->height = args->height;
    popover->visible = FALSE;

    // Create a popup-style window
    popover->window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_default_size(GTK_WINDOW(popover->window), args->width, args->height);
    gtk_window_set_resizable(GTK_WINDOW(popover->window), FALSE);
    gtk_window_set_decorated(GTK_WINDOW(popover->window), FALSE);
    gtk_window_set_skip_taskbar_hint(GTK_WINDOW(popover->window), TRUE);
    gtk_window_set_skip_pager_hint(GTK_WINDOW(popover->window), TRUE);
    gtk_window_set_keep_above(GTK_WINDOW(popover->window), TRUE);
    gtk_window_set_type_hint(GTK_WINDOW(popover->window), GDK_WINDOW_TYPE_HINT_POPUP_MENU);

    // Connect focus-out for click-outside dismissal
    g_signal_connect(popover->window, "focus-out-event", G_CALLBACK(on_focus_out), popover);

    // Create WebKitWebView
    WebKitUserContentManager *content_manager = webkit_user_content_manager_new();

    // Register script message handlers
    g_signal_connect(content_manager, "script-message-received::onwatchResize",
                     G_CALLBACK(on_script_message), popover);
    g_signal_connect(content_manager, "script-message-received::onwatchAction",
                     G_CALLBACK(on_script_message), popover);
    webkit_user_content_manager_register_script_message_handler(content_manager, "onwatchResize");
    webkit_user_content_manager_register_script_message_handler(content_manager, "onwatchAction");

    popover->webview = WEBKIT_WEB_VIEW(webkit_web_view_new_with_user_content_manager(content_manager));

    // Set transparent background
    GdkRGBA transparent = {0.0, 0.0, 0.0, 0.0};
    webkit_web_view_set_background_color(popover->webview, &transparent);

    // Configure navigation policy
    g_signal_connect(popover->webview, "decide-policy", G_CALLBACK(on_decide_policy), popover);

    gtk_container_add(GTK_CONTAINER(popover->window), GTK_WIDGET(popover->webview));
    gtk_widget_show(GTK_WIDGET(popover->webview));

    *args->result = popover;
}

void *onwatch_popover_create(int width, int height) {
    OnWatchPopover *result = NULL;
    CreateArgs args = {&result, width, height};
    onwatch_run_on_main_sync(do_create, &args);
    return (void *)result;
}

typedef struct {
    OnWatchPopover *popover;
} PopoverArg;

static void do_destroy(void *data) {
    PopoverArg *arg = (PopoverArg *)data;
    OnWatchPopover *popover = arg->popover;
    if (!popover) return;

    if (popover->window) {
        gtk_widget_destroy(popover->window);
        popover->window = NULL;
    }
    popover->webview = NULL;
    popover->visible = FALSE;
    free(popover);
}

void onwatch_popover_destroy(void *handle) {
    if (!handle) return;
    PopoverArg arg = {(OnWatchPopover *)handle};
    onwatch_run_on_main_sync(do_destroy, &arg);
}

typedef struct {
    OnWatchPopover *popover;
    gboolean result;
} ShowArgs;

static void position_at_tray(OnWatchPopover *popover) {
    GdkDisplay *display = gdk_display_get_default();
    if (!display) return;

    GdkMonitor *monitor = gdk_display_get_primary_monitor(display);
    if (!monitor) {
        monitor = gdk_display_get_monitor(display, 0);
    }
    if (!monitor) return;

    GdkRectangle workarea;
    gdk_monitor_get_workarea(monitor, &workarea);

    // Position at top-right of workarea with small offset (standard GNOME tray position)
    int x = workarea.x + workarea.width - popover->width - 8;
    int y = workarea.y + 4;

    gtk_window_move(GTK_WINDOW(popover->window), x, y);
}

static void do_show(void *data) {
    ShowArgs *args = (ShowArgs *)data;
    OnWatchPopover *popover = args->popover;
    if (!popover || !popover->window) {
        args->result = FALSE;
        return;
    }

    position_at_tray(popover);
    gtk_widget_show(popover->window);
    gtk_window_present(GTK_WINDOW(popover->window));
    gtk_widget_grab_focus(popover->window);
    popover->visible = TRUE;
    args->result = TRUE;
}

bool onwatch_popover_show(void *handle) {
    if (!handle) return false;
    ShowArgs args = {(OnWatchPopover *)handle, FALSE};
    onwatch_run_on_main_sync(do_show, &args);
    return args.result;
}

static void do_toggle(void *data) {
    ShowArgs *args = (ShowArgs *)data;
    OnWatchPopover *popover = args->popover;
    if (!popover || !popover->window) {
        args->result = FALSE;
        return;
    }

    if (popover->visible) {
        gtk_widget_hide(popover->window);
        popover->visible = FALSE;
        args->result = TRUE;
        return;
    }

    position_at_tray(popover);
    gtk_widget_show(popover->window);
    gtk_window_present(GTK_WINDOW(popover->window));
    gtk_widget_grab_focus(popover->window);
    popover->visible = TRUE;
    args->result = TRUE;
}

bool onwatch_popover_toggle(void *handle) {
    if (!handle) return false;
    ShowArgs args = {(OnWatchPopover *)handle, FALSE};
    onwatch_run_on_main_sync(do_toggle, &args);
    return args.result;
}

typedef struct {
    OnWatchPopover *popover;
    const char *url;
} LoadURLArgs;

static void do_load_url(void *data) {
    LoadURLArgs *args = (LoadURLArgs *)data;
    if (!args->popover || !args->popover->webview || !args->url) return;
    webkit_web_view_load_uri(args->popover->webview, args->url);
}

void onwatch_popover_load_url(void *handle, const char *url) {
    if (!handle || !url) return;
    LoadURLArgs args = {(OnWatchPopover *)handle, url};
    onwatch_run_on_main_sync(do_load_url, &args);
}

static void do_close(void *data) {
    PopoverArg *arg = (PopoverArg *)data;
    if (!arg->popover || !arg->popover->window) return;
    if (arg->popover->visible) {
        gtk_widget_hide(arg->popover->window);
        arg->popover->visible = FALSE;
    }
}

void onwatch_popover_close(void *handle) {
    if (!handle) return;
    PopoverArg arg = {(OnWatchPopover *)handle};
    onwatch_run_on_main_sync(do_close, &arg);
}

typedef struct {
    OnWatchPopover *popover;
    gboolean result;
} IsShownArgs;

static void do_is_shown(void *data) {
    IsShownArgs *args = (IsShownArgs *)data;
    args->result = args->popover && args->popover->visible;
}

bool onwatch_popover_is_shown(void *handle) {
    if (!handle) return false;
    IsShownArgs args = {(OnWatchPopover *)handle, FALSE};
    onwatch_run_on_main_sync(do_is_shown, &args);
    return args.result;
}
