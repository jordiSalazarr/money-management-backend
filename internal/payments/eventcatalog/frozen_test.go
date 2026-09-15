package eventcatalog_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/eventcatalog"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

var update = flag.Bool("update", false, "write missing golden fixtures instead of failing")

// TestEventShapesAreFrozen is the mechanism behind "bump the version on every
// shape change". A policy that relies on remembering produces v2 data stamped
// v1, which is worse than no versioning at all — the upcaster chain will
// confidently mistranslate it.
//
// Each fixture is also the exact input an upcaster test needs once that
// version becomes historical. One mechanism, two jobs.
//
// When this fails because you changed a shape deliberately:
//  1. bump Version for that entry in catalog.go
//  2. go test ./internal/payments/eventcatalog -update
//  3. commit the new fixture; the old one stays, frozen, forever
func TestEventShapesAreFrozen(t *testing.T) {
	for _, entry := range eventcatalog.Entries() {
		name := entry.Sample.EventType()
		t.Run(fmt.Sprintf("%s v%d", name, entry.Version), func(t *testing.T) {
			got, err := json.MarshalIndent(entry.Sample, "", "  ")
			if err != nil {
				t.Fatalf("marshalling sample: %v", err)
			}
			got = append(got, '\n')

			path := filepath.Join("testdata", fmt.Sprintf("%s.v%d.json", name, entry.Version))
			want, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				if *update {
					if err := os.MkdirAll("testdata", 0o755); err != nil {
						t.Fatalf("creating testdata: %v", err)
					}
					if err := os.WriteFile(path, got, 0o644); err != nil {
						t.Fatalf("writing fixture: %v", err)
					}
					t.Logf("wrote %s", path)
					return
				}
				t.Fatalf("no fixture at %s.\n"+
					"If %s is new, run: go test ./internal/payments/eventcatalog -update",
					path, name)
			}
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}

			if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
				t.Errorf("the shape of %s changed but it is still declared v%d.\n\n"+
					"Bump Version in catalog.go and regenerate with -update.\n"+
					"The old fixture stays: it is the input to the v%d->v%d upcaster.\n\n"+
					"want (%s):\n%s\ngot:\n%s",
					name, entry.Version, entry.Version, entry.Version+1,
					path, indent(want), indent(got))
			}
		})
	}
}

// TestEveryCatalogEntryIsDecodable closes the loop: the factory must produce
// the same shape the sample was marshalled from, or rehydration silently
// yields a zero value.
func TestEveryCatalogEntryIsDecodable(t *testing.T) {
	codec := eventcatalog.NewCodec()

	for _, entry := range eventcatalog.Entries() {
		name := entry.Sample.EventType()
		t.Run(name, func(t *testing.T) {
			env, err := codec.Encode(entry.Sample, "corr-1", time.Unix(0, 0).UTC())
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if env.SchemaVersion != entry.Version {
				t.Fatalf("codec stamped v%d but the catalog says v%d", env.SchemaVersion, entry.Version)
			}

			got, err := codec.Decode(eventstore.Recorded{Event: env})
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", entry.Sample) {
				t.Fatalf("round trip changed the type: %T -> %T", entry.Sample, got)
			}
			// A round trip through the codec must be lossless.
			wantJSON, _ := json.Marshal(entry.Sample)
			gotJSON, _ := json.Marshal(got)
			if !bytes.Equal(wantJSON, gotJSON) {
				t.Errorf("round trip lost data:\nwant %s\ngot  %s", wantJSON, gotJSON)
			}
		})
	}
}

// TestCatalogHasNoDuplicateEventTypes guards the one mistake a single list
// cannot catch by reading: registering the same type twice, where the second
// silently wins.
func TestCatalogHasNoDuplicateEventTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range eventcatalog.Entries() {
		name := entry.Sample.EventType()
		if seen[name] {
			t.Errorf("%s appears twice in the catalog", name)
		}
		seen[name] = true
	}
}

func indent(b []byte) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "\n  ")
}
