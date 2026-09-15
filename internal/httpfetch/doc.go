// Package httpfetch reads the body of an HTTP resource with a size limit.
//
// The schema loaders and the SchemaStore catalog fetch share it, so both
// reject the same responses: any status but 200 OK and any body over
// [MaxSize].
package httpfetch
