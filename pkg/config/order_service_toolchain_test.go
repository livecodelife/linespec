package config

import (
	"regexp"
	"strconv"
	"testing"
)

// The order-service example has no committed Cargo.lock, so its Rust
// dependencies float. A recent ctutils release (0.4.3) requires rustc 1.87, so
// the builder stage must pin a toolchain of at least 1.87.
//
// This is a proxy for the real proof, which is the order-service image
// building with `docker build`. It only checks the pinned builder tag, not
// that the build succeeds; CI's Example LineSpec Tests job exercises the real
// build end to end.

var rustBuilderFrom = regexp.MustCompile(`(?m)^FROM\s+rust:(\S+)`)
var rustMajorMinor = regexp.MustCompile(`^(\d+)\.(\d+)`)

func TestOrderServiceBuilderToolchainIsRecentEnough(t *testing.T) {
	dockerfile := readRepoFile(t, "../../examples/order-service/Dockerfile")

	m := rustBuilderFrom.FindStringSubmatch(dockerfile)
	if m == nil {
		t.Fatalf("no `FROM rust:<tag>` builder line found in examples/order-service/Dockerfile")
	}
	tag := m[1]

	v := rustMajorMinor.FindStringSubmatch(tag)
	if v == nil {
		t.Fatalf("cannot parse major.minor from builder tag rust:%s", tag)
	}
	major, _ := strconv.Atoi(v[1])
	minor, _ := strconv.Atoi(v[2])

	if major < 1 || (major == 1 && minor < 87) {
		t.Errorf("order-service builder pins rust:%s (%d.%d); ctutils@0.4.3 requires rustc 1.87 and the example has no Cargo.lock so dependencies float; want rust >= 1.87",
			tag, major, minor)
	}
}
