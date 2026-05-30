//go:build darwin

package autotype

// #cgo LDFLAGS: -framework Carbon -framework ApplicationServices
//
// #include <ApplicationServices/ApplicationServices.h>
// #include <Carbon/Carbon.h>
//
// static void cgevent_cmd_v(void) {
// 	CGEventRef cmdDown = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, true);  // kVK_Command
// 	CGEventRef vDown   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, true);   // kVK_ANSI_V
// 	CGEventRef vUp     = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, false);
// 	CGEventRef cmdUp   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, false);
//
// 	CGEventSetFlags(vDown, kCGEventFlagMaskCommand);
// 	CGEventSetFlags(vUp, kCGEventFlagMaskCommand);
//
// 	CGEventPost(kCGSessionEventTap, cmdDown);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, vDown);
// 	usleep(30000);
// 	CGEventPost(kCGSessionEventTap, vUp);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, cmdUp);
//
// 	CFRelease(cmdDown); CFRelease(vDown); CFRelease(vUp); CFRelease(cmdUp);
// }
import "C"

func simulatePaste() error {
	C.cgevent_cmd_v()
	return nil
}

func pasteMethod() string { return "darwin/CGEventPost+Cmd+V" }

func isWaylandSession() bool { return false }
