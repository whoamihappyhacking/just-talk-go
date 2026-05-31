#include "clipboard_darwin.h"

#import <AppKit/AppKit.h>
#include <stdlib.h>

int jt_clipboard_set(const char *text) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		NSString *s = [NSString stringWithUTF8String:text == NULL ? "" : text];
		return [pb setString:s forType:NSPasteboardTypeString] ? 0 : -1;
	}
}

char *jt_clipboard_get(void) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSString *s = [pb stringForType:NSPasteboardTypeString];
		if (s == nil) {
			return strdup("");
		}
		return strdup([s UTF8String]);
	}
}
