// Package httpfetch reads the body of an HTTP resource with a size limit.
//
// The schema loaders and the SchemaStore catalog fetch share it. Each of
// them refuses any status other than 200 OK, and any body of more than
// the limit that its caller gives.
package httpfetch
