#pragma once

void *jt_overlay_create(const char *position, double scale);
void jt_overlay_show(void *handle, const char *label, unsigned short r, unsigned short g, unsigned short b);
void jt_overlay_hide(void *handle);
void jt_overlay_close(void *handle);
void jt_overlay_helper_init(const char *position, double scale);
void jt_overlay_helper_run_app(void);
void jt_overlay_run_helper(const char *position, double scale);
void jt_overlay_helper_show(const char *label, unsigned short r, unsigned short g, unsigned short b);
void jt_overlay_helper_hide(void);
void jt_overlay_helper_close(void);
