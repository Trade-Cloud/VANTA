//go:build darwin

// Package dockicon ставит иконку приложения в док macOS.
//
// Fyne (GLFW) на macOS иконку окна игнорирует, поэтому при запуске без
// .app-бандла (go run / go build) в доке висит стандартная иконка.
// Здесь ставим её напрямую через NSApp.
package dockicon

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void vantaSetDockIcon(const void *data, int length) {
	NSData *d = [NSData dataWithBytes:data length:length];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSImage *img = [[NSImage alloc] initWithData:d];
		if (img != nil) {
			[NSApp setApplicationIconImage:img];
			[img release];
		}
	});
}
*/
import "C"

import "unsafe"

// Set ставит PNG как иконку в доке. Вызывать после старта приложения.
func Set(png []byte) {
	if len(png) == 0 {
		return
	}
	C.vantaSetDockIcon(unsafe.Pointer(&png[0]), C.int(len(png)))
}
