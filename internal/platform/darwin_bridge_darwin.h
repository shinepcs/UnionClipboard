#ifndef DARWIN_BRIDGE_H
#define DARWIN_BRIDGE_H

#include <stdint.h>
#include <stdbool.h>

void uc_run_app(void);
void uc_quit_app(void);
bool uc_register_hotkey(bool ctrl, bool alt, bool shift, bool cmd, const char *keyName);
void uc_read_clipboard(void);
void uc_write_clipboard_text(const char *utf8Text);
void uc_write_clipboard_png(const void *pngBytes, int len);
void uc_write_clipboard_files(const char **paths, int count);

void uc_popup_reset(void);
void uc_popup_add_item(int tag, const char *title, const void *thumbPNG, int thumbLen, const char *keyEquiv, bool enabled);
void uc_popup_add_separator(void);
void uc_popup_begin_submenu(const char *title, const char *keyEquiv);
void uc_popup_end_submenu(void);
void uc_popup_show(void);

void uc_send_paste(void);
void uc_open_viewer(const char *url);
void uc_update_tray(int onlinePeers, int totalPeers, bool universal, bool paused, const char *hotkey);
void uc_show_notification(const char *title, const char *body);
bool uc_has_accessibility(void);
void uc_request_accessibility(void);
void uc_reveal_paths(const char **paths, int count);

// Callbacks implemented in Go
extern void goOnReady(void);
extern void goOnClipboardChange(int kind, char *text, void *pngBytes, int pngLen, char *pathsJoined, char *fgBundle, int sensitive);
extern void goOnHotkey(void);
extern void goOnPopupTag(int tag);
extern void goOnOpenViewer(void);
extern void goOnToggleUniversal(void);
extern void goOnTogglePause(void);
extern void goOnQuit(void);

#endif
