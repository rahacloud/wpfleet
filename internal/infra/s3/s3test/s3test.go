// Package s3test is a tiny S3 service for tests: one bucket whose listing
// answers ListObjectsV2 and GetBucketLocation, enough for minio-go to list
// backups. It never stores anything.
package s3test

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// AccessKeyID is the access key the server expects in the signature.
const AccessKeyID = "test-access-key"

// SecretAccessKey goes with AccessKeyID; the server does not check
// signatures.
const SecretAccessKey = "test-secret-key"

// Server is the fake service.
type Server struct {
	*httptest.Server

	bucket  string
	objects map[string]int64
	lists   atomic.Int32
}

// New starts a server with one bucket holding objects (key to size). It stops
// when the test ends.
func New(tb testing.TB, bucket string, objects map[string]int64) *Server {
	tb.Helper()

	s := &Server{Server: nil, bucket: bucket, objects: objects, lists: atomic.Int32{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.Close)

	return s
}

// Lists is how many listings the server answered.
func (s *Server) Lists() int {
	return int(s.lists.Load())
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+AccessKeyID+"/") {
		http.Error(w, "unsigned or wrong key", http.StatusForbidden)

		return
	}

	if r.Method != http.MethodGet || strings.Trim(r.URL.Path, "/") != s.bucket {
		http.Error(w, "no such bucket", http.StatusNotFound)

		return
	}

	q := r.URL.Query()

	switch {
	case q.Has("location"):
		writeXML(w, locationConstraint{XMLName: xml.Name{Space: ns, Local: "LocationConstraint"}, Region: ""})
	case q.Get("list-type") == "2":
		s.lists.Add(1)
		writeXML(w, s.listing(q.Get("prefix")))
	default:
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

// listing returns every object under prefix in one page.
func (s *Server) listing(prefix string) listBucketResult {
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	contents := make([]object, 0, len(keys))
	for _, k := range keys {
		contents = append(contents, object{
			Key:          k,
			LastModified: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			ETag:         `"0"`,
			Size:         s.objects[k],
			StorageClass: "STANDARD",
		})
	}

	return listBucketResult{
		XMLName:     xml.Name{Space: ns, Local: "ListBucketResult"},
		Name:        s.bucket,
		Prefix:      prefix,
		KeyCount:    strconv.Itoa(len(contents)),
		MaxKeys:     "1000",
		IsTruncated: false,
		Contents:    contents,
	}
}

const ns = "http://s3.amazonaws.com/doc/2006-03-01/"

type locationConstraint struct {
	XMLName xml.Name
	Region  string `xml:",chardata"`
}

type listBucketResult struct {
	XMLName     xml.Name
	Name        string   `xml:"Name"`
	Prefix      string   `xml:"Prefix"`
	KeyCount    string   `xml:"KeyCount"`
	MaxKeys     string   `xml:"MaxKeys"`
	IsTruncated bool     `xml:"IsTruncated"`
	Contents    []object `xml:"Contents"`
}

type object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// writeXML writes v as an S3 response.
func writeXML[T locationConstraint | listBucketResult](w http.ResponseWriter, v T) {
	data, err := xml.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(append([]byte(xml.Header), data...)) //nolint:gosec // XML a test server marshalled itself, not HTML
}
