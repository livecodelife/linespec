package registry

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

// A hit is recorded by a proxy sidecar and read back by the host, and the only
// thing carrying it across is the string mockHitKey returns. So the key has to
// distinguish every mock the matcher can distinguish: where two mocks share a
// key, a hit on one satisfies the other on the host side, and the never-called
// check — the only assertion that an outbound call actually happened — is inert
// for whichever the service skipped.
//
// Each test here drives the container-side registry through its real matcher,
// hands the reported hits to a host-side registry built from the same spec, and
// asks VerifyAll what it concludes. That is the exact path the runner takes
// (collectHits → SetHits → VerifyAll), and it is the only place the collision
// is visible — proxy-side matching was always correct.

// mergeAndVerify reconstructs the host's view of a spec, merges in the hits a
// proxy reported, and returns what VerifyAll concludes.
func mergeAndVerify(spec *types.TestSpec, hits map[string]int) error {
	hostSide := NewMockRegistry()
	hostSide.Register(spec)
	hostSide.SetHits(hits)
	return hostSide.VerifyAll()
}

// An insert asserted alongside the read that names the row its unique
// constraint refused: two expectations on one REST collection, differing only
// in method. A read-first implementation makes the GET and never attempts the
// POST, and the spec must fail on the POST rather than pass.
func TestMockHitKey_HTTPDistinguishesByMethod(t *testing.T) {
	const url = "http://dep:8080/rest/v1/skill_versions"

	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "duplicate_upload_is_refused_by_the_constraint",
			Expects: []types.ExpectStatement{
				{Channel: types.HTTP, URL: url, Method: "POST", CallN: 1},
				{Channel: types.HTTP, URL: url, Method: "GET", CallN: 1},
			},
		}
	}

	containerSide := NewMockRegistry()
	spec := newSpec()
	containerSide.Register(spec)

	if _, found := containerSide.FindHTTPMock(url, "GET"); !found {
		t.Fatal("expected the GET mock to match")
	}

	hits := containerSide.GetHits()
	if len(hits) != 1 {
		t.Fatalf("expected exactly one distinct hit key after one GET, got %d: %v", len(hits), hits)
	}

	if err := mergeAndVerify(newSpec(), hits); err == nil {
		t.Fatal("expected VerifyAll to fail: the POST was never made, so the insert was never attempted")
	}

	if _, found := containerSide.FindHTTPMock(url, "POST"); !found {
		t.Fatal("expected the POST mock to match")
	}
	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass once both calls were made, got: %v", err)
	}
}

// The same collision in the other direction. A negative expectation must be
// satisfied only by a call the matcher attributed to it — a POST the spec
// asked for cannot be what fails an EXPECT_NOT on the GET.
func TestMockHitKey_HTTPNegativeIsNotSatisfiedByAnotherMethod(t *testing.T) {
	const url = "http://dep:8080/rest/v1/skill_versions"

	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "the_insert_happens_and_nothing_reads_back",
			Expects: []types.ExpectStatement{
				{Channel: types.HTTP, URL: url, Method: "POST", CallN: 1},
			},
			ExpectsNot: []types.ExpectStatement{
				{Channel: types.HTTP, URL: url, Method: "GET"},
			},
		}
	}

	containerSide := NewMockRegistry()
	containerSide.Register(newSpec())

	containerSide.CheckNegativeHTTPMocks(url, "POST")
	if _, found := containerSide.FindHTTPMock(url, "POST"); !found {
		t.Fatal("expected the POST mock to match")
	}

	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass: only the POST was made, and the EXPECT_NOT names the GET; got: %v", err)
	}
}

// Two calls to one RPC, disambiguated by CALL N. The matcher consumes a mock
// once and takes them in declaration order, so it can tell the second from the
// first; the key must too.
func TestMockHitKey_GRPCDistinguishesByCallN(t *testing.T) {
	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "two_calls_to_one_rpc",
			Expects: []types.ExpectStatement{
				{Channel: types.GRPC, Service: "users.UserService", RPCMethod: "GetUser", CallN: 1},
				{Channel: types.GRPC, Service: "users.UserService", RPCMethod: "GetUser", CallN: 2},
			},
		}
	}

	containerSide := NewMockRegistry()
	containerSide.Register(newSpec())

	if _, found := containerSide.FindGRPCMock("users.UserService", "GetUser"); !found {
		t.Fatal("expected the first mock to match")
	}

	hits := containerSide.GetHits()
	if len(hits) != 1 {
		t.Fatalf("expected exactly one distinct hit key after one call, got %d: %v", len(hits), hits)
	}
	if err := mergeAndVerify(newSpec(), hits); err == nil {
		t.Fatal("expected VerifyAll to fail: the second call was never made")
	}

	if _, found := containerSide.FindGRPCMock("users.UserService", "GetUser"); !found {
		t.Fatal("expected the second mock to match")
	}
	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass once both calls were made, got: %v", err)
	}
}

func TestMockHitKey_RedisDistinguishesByCallN(t *testing.T) {
	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "two_reads_of_one_key",
			Expects: []types.ExpectStatement{
				{Channel: types.ReadRedis, Command: "GET", RedisKey: "user:123", CallN: 1},
				{Channel: types.ReadRedis, Command: "GET", RedisKey: "user:123", CallN: 2},
			},
		}
	}

	containerSide := NewMockRegistry()
	containerSide.Register(newSpec())

	if _, found := containerSide.FindRedisMock("GET", "user:123"); !found {
		t.Fatal("expected the first mock to match")
	}

	hits := containerSide.GetHits()
	if len(hits) != 1 {
		t.Fatalf("expected exactly one distinct hit key after one read, got %d: %v", len(hits), hits)
	}
	if err := mergeAndVerify(newSpec(), hits); err == nil {
		t.Fatal("expected VerifyAll to fail: the second read never happened")
	}

	if _, found := containerSide.FindRedisMock("GET", "user:123"); !found {
		t.Fatal("expected the second mock to match")
	}
	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass once both reads happened, got: %v", err)
	}
}

// MongoDB falls to mockHitKey's default arm, which keyed on channel and
// collection alone — dropping both the database and CALL N, the two things
// FindMock discriminates on for this channel.
func TestMockHitKey_MongoDBDistinguishesByCallN(t *testing.T) {
	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "two_finds_on_one_collection",
			Expects: []types.ExpectStatement{
				{Channel: types.ReadMongoDB, Table: "orders", CallN: 1},
				{Channel: types.ReadMongoDB, Table: "orders", CallN: 2},
			},
		}
	}

	containerSide := NewMockRegistry()
	containerSide.Register(newSpec())

	if _, found := containerSide.FindMock("orders", "FIND"); !found {
		t.Fatal("expected the first mock to match")
	}

	hits := containerSide.GetHits()
	if len(hits) != 1 {
		t.Fatalf("expected exactly one distinct hit key after one find, got %d: %v", len(hits), hits)
	}
	if err := mergeAndVerify(newSpec(), hits); err == nil {
		t.Fatal("expected VerifyAll to fail: the second find never happened")
	}

	if _, found := containerSide.FindMock("orders", "FIND"); !found {
		t.Fatal("expected the second mock to match")
	}
	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass once both finds happened, got: %v", err)
	}
}

// Two expectations on one collection served by different logical databases.
// FindMock filters on the database, so the key must carry it.
func TestMockHitKey_MongoDBDistinguishesByDatabase(t *testing.T) {
	newSpec := func() *types.TestSpec {
		return &types.TestSpec{
			Name: "one_collection_in_two_databases",
			Expects: []types.ExpectStatement{
				{Channel: types.ReadMongoDB, Table: "orders", Database: "district"},
				{Channel: types.ReadMongoDB, Table: "orders", Database: "master"},
			},
		}
	}

	containerSide := NewMockRegistry()
	containerSide.Register(newSpec())

	if _, found := containerSide.FindMock("orders", "FIND", "district"); !found {
		t.Fatal("expected the district mock to match")
	}

	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err == nil {
		t.Fatal("expected VerifyAll to fail: the master database was never read")
	}

	if _, found := containerSide.FindMock("orders", "FIND", "master"); !found {
		t.Fatal("expected the master mock to match")
	}
	if err := mergeAndVerify(newSpec(), containerSide.GetHits()); err != nil {
		t.Errorf("expected VerifyAll to pass once both databases were read, got: %v", err)
	}
}
