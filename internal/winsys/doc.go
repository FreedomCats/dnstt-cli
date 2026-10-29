// Package winsys is the Windows system layer: choosing the physical interface, routes,
// DNS and subscribing to network changes.
//
// The package code lives in *_windows.go files, and only they import winipcfg from
// golang.zx2c4.com/wireguard/windows. On other systems the package is empty.
package winsys
