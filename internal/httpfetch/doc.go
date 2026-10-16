// Package httpfetch reads the body of an HTTP resource with a size limit.
//
// The schema loaders and the SchemaStore catalog fetch share it. Both
// refuse any status but 200 OK, and each refuses a body of more than the
// limit its caller gives.
package httpfetch
