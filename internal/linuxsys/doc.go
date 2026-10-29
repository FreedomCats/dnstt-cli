// Package linuxsys is the Linux system layer: choosing the physical interface,
// subscribing to network changes, the adapter, routes and DNS. Counterpart of winsys.
//
// The package code lives in *_linux.go files, and only they import netlink from
// github.com/vishvananda/netlink. On other systems the package is empty.
package linuxsys
