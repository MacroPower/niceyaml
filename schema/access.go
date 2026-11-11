package schema

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

var (
	// Unrestricted is the [*Access] that allows every file on disk and
	// every HTTP or HTTPS URL, as [AllowAnyFile] and [AllowAnyURL] do
	// together. It suits a program whose documents and schemas come from
	// its own user, such as an editor or a command-line tool. A directive
	// there names local paths and URLs, as it does in
	// yaml-language-server. Such a program passes Unrestricted to
	// [WithAccess] where it builds the registry:
	//
	//	reg := schema.NewRegistry(
	//	    schema.WithAccess(schema.Unrestricted),
	//	    schema.WithResolvers(schema.Directive()),
	//	)
	//
	// A registry takes a copy of the Access when the program passes it.
	// Assigning to Unrestricted does not change a registry that already
	// took it or a registry built without WithAccess.
	Unrestricted = &Access{accessConfig{anyFile: true, anyURL: true}}

	// NoAccess is the [*Access] that does not allow any file on disk or
	// any URL, as the zero Access does. It suits a program whose documents
	// live in a file system, such as an [embed.FS] or the archive of an
	// upload. A directive there reads the schema file that it names from
	// the file system of its document, as Access describes. A directive
	// that names a URL fails to load with [ErrURLNotAllowed]:
	//
	//	reg := schema.NewRegistry(
	//	    schema.WithAccess(schema.NoAccess),
	//	    schema.WithResolvers(schema.Directive()),
	//	)
	//
	// The Access covers every resolver of the registry, so a [Ref] from
	// [File] or [URL] fails to load there too. A Ref from [FileFS],
	// [Embedded], or [Loadable] still loads.
	//
	// A registry takes a copy of the Access when the program passes it.
	// Assigning to NoAccess does not change a registry that already took
	// it.
	NoAccess = &Access{}

	// ErrAccessNotSet indicates that a document named a schema in a
	// registry built without [WithAccess]. [RefBeside] marks each [Ref]
	// that it builds for a document, and [Directive] names its schemas
	// through RefBeside. The author of the document picks the file or URL
	// of such a Ref. So the program must state what the registry reads and
	// fetches for it. A registry without the option refuses the Ref before
	// it checks its cache, reads a file, or sends a request.
	// [Registry.Schema] and [Registry.Load] return the refusal as a
	// [*LoadError] that matches [ErrLoad], and a lookup binds it at the
	// directive:
	//
	//	app.yaml:1:1: load schema: "https://example.com/s.json": registry needs schema.WithAccess to load a schema that a document names
	//
	// The program built the registry, so the document is not at fault, and
	// [niceyaml.IsInvalid] reports false for the error. A registry with an
	// Access reports a schema outside that Access with [ErrFileNotAllowed]
	// or [ErrURLNotAllowed] instead. A server can thus tell its own
	// missing option from a document that reached outside its Access.
	ErrAccessNotSet = errors.New("registry needs schema.WithAccess to load a schema that a document names")
)

// Access decides which files a [Registry] reads from disk and which URLs
// it fetches. [WithAccess] gives a registry an Access. The registry
// checks it for every load: the schema that a [Ref] names, each document
// that a $ref names, and each hop of a redirect. A file or URL that the
// Access does not allow fails to load. The registry does not read the
// file or send a request for the URL.
//
// The zero Access does not allow any file on disk or any URL, and
// neither does [NoAccess] or a nil *Access. [NewAccess] allows what its
// options allow, and [Unrestricted] allows every file and every HTTP or
// HTTPS URL:
//
//	root, err := os.OpenRoot(tenantDir)
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	access, err := schema.NewAccess(
//	    schema.AllowRoot(root),
//	    schema.AllowURLPrefixes("https://schemas.example.com/"),
//	)
//	if err != nil {
//	    return err
//	}
//
//	reg := schema.NewRegistry(
//	    schema.WithAccess(access),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// The options for the disk and those for the network combine freely.
// Some hosted validators fetch every URL that a directive names, as
// yaml-language-server does, but do not read any file of their server.
// Such a validator takes NewAccess(AllowAnyURL()). It then keeps the
// fetches off its own network through the Dialer of the client that it
// gives [WithHTTPClient].
//
// An Access covers the disk and the network. A Ref from [FileFS], and a
// reference beside a document from [niceyaml.NewSourceFromFS] or
// [niceyaml.WithFS], read from that file system. The Access never sees
// those reads, even an Access that allows no file. The URLs that such a
// document names still go through the Access. A Ref from [Embedded] or
// [Loadable] loads its own bytes.
//
// For a document in a file system, the file system is the confinement of
// its files. A directive that names a path reads what the file system
// serves, and a path that leads out of it fails with [ErrFileNotAllowed].
// Give a document from another trust domain a file system that holds only
// the files of its author. The [*archive/zip.Reader] of an upload holds
// them, and so does the FS of an [os.Root] on the directory of the upload.
// An [os.DirFS] follows a symbolic link out of its directory, so a link in
// the upload reads any file that the process can read.
//
// A registry applies one Access to every resolver that it holds. It
// keeps each schema that it loads under that Access. A program that
// trusts the schemas that a document names less than its own schemas
// keeps the two kinds of schemas in separate registries. One registry is
// then a resolver of the other, as [Registry.Resolve] describes. The
// Untrusted Documents section of the package documentation gives the
// whole recipe.
//
// Create instances with [NewAccess].
type Access struct {
	accessConfig
}

// AccessOption configures [Access] creation.
//
// Available options:
//   - [AllowRoot]
//   - [AllowAnyFile]
//   - [AllowURLPrefixes]
//   - [AllowAnyURL]
type AccessOption func(*accessConfig) error

// accessConfig holds the settings that an [AccessOption] configures. An
// option takes it in place of the [Access] that embeds it, so only
// [NewAccess] can apply one.
type accessConfig struct {
	// The file system of the root of AllowRoot. The registry reads each
	// file on disk through it. It stays nil until AllowRoot sets it.
	fsys fs.FS
	// The directory on disk that the root of fsys stands for.
	dir *fsDir
	// The prefixes of AllowURLPrefixes. The Access allows each URL under
	// them.
	prefixes []urlPrefix
	// Whether the registry reads any file on disk, as AllowAnyFile allows.
	anyFile bool
	// Whether the registry fetches any HTTP or HTTPS URL, as AllowAnyURL
	// allows.
	anyURL bool
}

// NewAccess creates a new [*Access] that allows what opts allow and
// nothing more. Without options, the Access matches the zero Access and
// does not allow any file on disk or any URL:
//
//	access, err := schema.NewAccess()
//
// Returns the errors of every option that fails, joined. Each error names
// its option.
func NewAccess(opts ...AccessOption) (*Access, error) {
	a := &Access{}

	var errs []error

	for _, opt := range opts {
		err := opt(&a.accessConfig)
		if err != nil {
			errs = append(errs, err)
		}
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	return a, nil
}

// AllowRoot is an [AccessOption] that allows the files under the
// directory that root holds. It serves every [Ref] that names a file on
// disk, such as one from [File]. [Directive], [RefBeside], and
// [FileOrURL] build such a Ref for a document on disk. The registry
// resolves each path as it does under [Unrestricted], so a relative path
// resolves against the working directory at the time File runs. It then
// reads the path relative to the directory through root. A path outside
// the directory does not name a file, so it fails to load with
// [ErrFileNotAllowed]. Off Windows, a path with a drive letter fails the
// same way.
//
// The registry reads only regular files within the limit of
// [WithLoadLimit], as [File] describes. The registry reads through root,
// so the program must keep root open while a registry uses the Access.
//
// [NewAccess] returns an error for a nil root, a closed root, a second
// root in one Access, and a root beside [AllowAnyFile]. It also returns
// one for a root whose name names another directory, as Directory Paths
// describes.
//
// # Symbolic Links
//
// The registry reads through root, so a symbolic link inside the
// directory that leads out of it does not name a file. An absolute link
// does not name one either. The error of such a link is the error of the
// [os.Root], which does not match [ErrFileNotAllowed]. A link that stays
// inside the directory reads the file that it leads to.
//
// # Directory Paths
//
// The registry names the directory by the name that [os.OpenRoot] took,
// which [os.Root.Name] returns. [NewAccess] makes a relative name absolute
// against the working directory at the time it runs. NewAccess returns
// an error when that path does not name the directory that root holds,
// such as after the program changes its working directory between
// OpenRoot and NewAccess. When the process does not have a working
// directory, NewAccess cannot make a relative name absolute, so it
// returns an error too.
//
// The registry compares each path with the directory as written. When
// the path is not under the directory that way, the registry resolves
// the symbolic links in the directories of both and compares them again.
// A program that entered its working directory through a link therefore
// still reads the files under a root that it opened by its physical
// path.
//
// # Keys and URLs
//
// [Ref.Key], the cache, and the URL that a $ref resolves against stay
// the same as under [Unrestricted]. Each error names the path on disk.
func AllowRoot(root *os.Root) AccessOption {
	return func(c *accessConfig) error {
		if root == nil {
			return errors.New("schema.AllowRoot: root is nil")
		}

		if c.fsys != nil {
			return fmt.Errorf("schema.AllowRoot: %s: the access holds a root already", root.Name())
		}

		if c.anyFile {
			return fmt.Errorf("schema.AllowRoot: %s: the access allows every file already", root.Name())
		}

		dir, err := newFSDir(root)
		if err != nil {
			return fmt.Errorf("schema.AllowRoot: %w", err)
		}

		c.fsys, c.dir = root.FS(), dir

		return nil
	}
}

// AllowAnyFile is an [AccessOption] that allows every file on disk. The
// registry reads each path as [File] describes. A program that must
// confine only the URLs that it fetches, such as a command-line tool limited
// to the schema hosts of its organization, pairs it with
// [AllowURLPrefixes].
//
// [NewAccess] returns an error for AllowAnyFile beside [AllowRoot],
// because a root does not confine the registry when the Access allows
// every file.
func AllowAnyFile() AccessOption {
	return func(c *accessConfig) error {
		if c.fsys != nil {
			return errors.New("schema.AllowAnyFile: the access holds a root already")
		}

		c.anyFile = true

		return nil
	}
}

// AllowAnyURL is an [AccessOption] that allows every HTTP and HTTPS URL.
// The registry fetches each one with the client that [WithHTTPClient]
// gave it, and follows the redirects that the client follows. A program that must keep
// the fetches off its own network checks each address in the Dialer of
// that client.
//
// [NewAccess] returns an error for AllowAnyURL beside
// [AllowURLPrefixes], because the prefixes do not confine the registry
// when the Access allows every URL.
func AllowAnyURL() AccessOption {
	return func(c *accessConfig) error {
		if len(c.prefixes) > 0 {
			return errors.New("schema.AllowAnyURL: the access holds URL prefixes already")
		}

		c.anyURL = true

		return nil
	}
}

// WithAccess is a [RegistryOption] that sets which files the registry
// reads from disk and which URLs it fetches, as [Access] describes:
//
//	reg := schema.NewRegistry(
//	    schema.WithAccess(access),
//	    schema.WithCacheLimit(1024),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// A registry needs the option to load a [Ref] that a document names,
// which is a Ref from [RefBeside] or [Directive]. Without the option,
// such a Ref fails to load with [ErrAccessNotSet], and the registry does
// not read or fetch anything for it. A program whose documents come from
// its own user passes [Unrestricted]. A program whose documents live in a
// file system passes [NoAccess]. The Untrusted Documents section of the
// package documentation covers documents from another trust domain.
//
// The option does not gate the Refs that the resolvers of the program
// name, such as a Ref from [File], [URL], or [FileOrURL]. It does not
// gate a Ref that a
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] names from its
// catalog either. A registry without the option loads them, and the
// documents that their $refs and redirects name, as it does under
// Unrestricted.
//
// The registry tells the two kinds of Refs apart only by the mark of
// RefBeside. A resolver that reads a reference from a document and
// passes it to FileOrURL returns a Ref of the program. Such a resolver
// should call RefBeside instead.
//
// The zero Access does not allow any file on disk or any URL, and
// neither does an Access from [NewAccess] without options.
//
// Panics if a is nil. A nil Access comes only from an ignored error of
// [NewAccess]. The Access is a security boundary, so the option does not
// pick a default for it.
//
// The option keeps its own copy of a, so a later assignment to *a does
// not change the option. Given more than once, the last option wins.
func WithAccess(a *Access) RegistryOption {
	if a == nil {
		panic("schema.WithAccess: access is nil")
	}

	cfg := a.accessConfig

	return func(c *registryConfig) {
		c.access, c.accessSet = cfg, true
	}
}

// readFile returns the bytes of the file at path, an absolute path on
// disk, within limit, when c allows the file. Under [AllowAnyFile], it
// reads path from disk. Under [AllowRoot], it reads path relative to the
// directory of the root through the root, and a path outside that
// directory fails with [ErrFileNotAllowed]. For an Access that does not
// have either option, every path fails with ErrFileNotAllowed. Each
// error is a [*FetchError] that names path.
func (c *accessConfig) readFile(path string, limit int) ([]byte, error) {
	switch {
	case c.anyFile:
		return readDisk(path, limit)

	case c.fsys == nil:
		return nil, readError(path, fmt.Errorf(
			"%w: the access of the registry allows no file on disk", ErrFileNotAllowed,
		))

	default:
		name, err := c.dir.rel(path)
		if err != nil {
			return nil, readError(path, err)
		}

		return readFS(c.fsys, name, path, limit)
	}
}
