package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync/atomic"
)

// agentsviewTestFixtureHashes holds SHA-256 hashes for secret-shaped strings
// that appear in agentsview's own unit tests or historical development
// transcripts. The raw values are intentionally not stored here: committing a
// deny-list of literal tokens creates another source file that a recorded
// agent session can quote and then report as a leak.
//
// Adding a new positive-path secret fixture to a test? Add only its SHA-256
// hash here. The fixture hash list is folded into RulesVersion, so backfill
// treats deny-list changes as scanner changes and re-scans stale sessions.
var agentsviewTestFixtureHashes = map[string]struct{}{
	"039a47e1ca7c07d6514be7a9a51c185e6b0e276a2762ed6d8df0a134d36c3391": {},
	"05a943eac035fd10c9e844015aaab9f4c329f175d855154c9188b2a125279dd1": {},
	"06d5fd8a4cc77b936bbf716edc75f557de14ab56947db9d03072867df9620a7b": {},
	"08d4c233406d9209ddba77a3fbaa6be61a74dc1e4fa6e10a2b449c146256f551": {},
	"139ceecae377649080e50034d613a57e9725f4e042b8a97fe35d6d20e19c5aea": {},
	"155ffdd48c2b34fbcb328616efc689afa844dffaec0d57bc60456aa0db9105d2": {},
	"17c22292f944fcaba03e4bd70bc0871464590bd9eb47930fbc2a91a886c69346": {},
	"1b99c0d6422aa645d3562e482203704ca1d9114e5bffaf840dfeba9aadeff156": {},
	"1f53bf73ee2a7cbea869ae96428476a61d25b22f0e6f3ecc92384b09f452b3a2": {},
	"2c2c3f90a9afeb084629366c3a5a67b3e9fce9373744c1b971ae77f1b617b2e6": {},
	"378a8d661936c5c4f5bc20fbcb6ad7395e00c95fd059d4cceb2d56d54eaccff5": {},
	"3b2f3ed37a1d8b805753c1851d24470415ab1b63cb4c91522d9f17ea58b28801": {},
	"3b907a44b9bf0c631d38768fdbfa642091db43cc64e90a94b48b2aeb8c40214f": {},
	"531660540c834c1b12e79db2fc9936411648e234487c9000a89941d8c24f69ee": {},
	"569179513f9c76b5d1354200ffc38b24fc10bc7ee356a0ab8c9553433ca79745": {},
	"59304de2f7780c2ca6dcdeafa13f16ad6d11b3f249f118c76eede16a4dabddc2": {},
	"61ac8943ed72a9b6af5a7d578df224ddea357ecccc4a53a24bdeebe131647515": {},
	"68b2f57c29b2f4ed7faffe571045fdf7db65f5295609a083a9fc879dbfbb4949": {},
	"68f13b41b6aa48ecb511ac1ef2ccb29d172b71a22b0a5bbbf2a63d387b5ac423": {},
	"743554670c6065b3f7f13ac4f07e392f977b3556ceb7457411633c454bcbece8": {},
	"8d543022186694800a16415adeaae9cf24594ba29bda9ec7b8ae23d9e37654ff": {},
	"8f763c652c84662f521157a4fc775deb090103bed48467e6da00215c4490e41b": {},
	"9383d3a321ec3ee35cc104a887d56aa8cd3543fd29eae27b40cebc14354bfc2e": {},
	"93af5d4adbd2b4ec5940ad35caa0fad111157a2eb9c3476a33e7be7f92237cc5": {},
	"9a0b5b544911419929246614f134083d67d6e58fb2eed48cbf14d349ec3fc1e6": {},
	"9a7ec609dc2b71a06470d9af744fab4bdc8040288adc562845d54a01f3ca36c8": {},
	"a2c7ceda4b9bbac471dad785bc70f41404a3c5023f20b17dcafa4145e9a0b633": {},
	"af8207cf94138ad22f72a35b81ee53668fe8d3d14e21cf4aa29f7e5c5e12cddd": {},
	"b2d1ca1a67caf3326751b12ef347a5c525d680319a0513ff836398c3182242a7": {},
	"b9e542bb492ae97fb2006d797da5b389a1237674d713c49eabc1ff6d99bec67c": {},
	"bdcb1b818a7ba4b3c5c7c421b9c6279beb34df45f7abab1503c6d150533ad642": {},
	"c1589e1eece8695f238be33923fcd4cbc845b34fb792ef34f9b698beffbd5324": {},
	"c14f984e31e231914767b815b1e9c71acd3825cb1c20bda419b39a0d60db9eb5": {},
	"c7d894bcb8b6ae7416f2aa6451723d1ac7b17c999edbe03849f63159e8e2f53a": {},
	"ca3533eaa36311ec950e1443330a7b0b4a452f247f4f478f419a469a95d2cae8": {},
	"cfae9a6eb09ea990634453c17ad47f9b3da587dd78e0e5db60238802f2efbedb": {},
	"d320c7f826ef94b4c2f9353597bc2a70669e4cb49eea15e11a504372e05034a6": {},
	"db744f2869684e66ab10c80e6ac8d0f4dfce158d5451ea30318e7fa1e9bd7471": {},
	"ddde2240b099bf523beab304d905a2b62490a2c9169ba7eb374210805d56653c": {},
	"df26a79256a73d9b7c014e9ea73372498e70a6072d22c0386ed3c6edea9817ac": {},
	"e38b3c3f77755fd8f5d4dd1c8e9f305313ea15ab8144432e08ac4b1a08f631d6": {},
	"e40d52868dd5a755e22a235de77b057bb08e7131a7e7a895ad6468d420bc3d9a": {},
	"f1287e2b02f273ad2f7403acf028b9ade16e8cf89141ba1889e13bf200d9905a": {},
	"f6f1f5ef34148842e3e8aaf7398be34d6157bc7fae6c782f539ec9afa8ed7eab": {},
	"f7737a917ad5dae7d11141f39dee668ef687d3e0a7fa0e27a3cffba7fcfa8da4": {},
	"fbe68dd1d502fded62be57881c10ea10fdf5205f20bbaeff8096e1e3c2e2309a": {},
}

// isAgentsviewTestFixture reports whether s is one of agentsview's known
// secret-shaped test fixtures.
func isAgentsviewTestFixture(s string) bool {
	_, ok := agentsviewTestFixtureHashes[fixtureHash(s)]
	return ok
}

func fixtureHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sortedAgentsviewFixtureHashes() []string {
	out := make([]string, 0, len(agentsviewTestFixtureHashes))
	for h := range agentsviewTestFixtureHashes {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// fixtureDenyEnabled controls whether Scan filters matches against
// agentsviewTestFixtureHashes. False by default so unit tests (which build
// their input around random-looking fixtures the rules need to verify positive
// paths against) pass without per-test boilerplate. The agentsview binary calls
// EnableFixtureDeny at startup so production scans automatically suppress
// agentsview's own fixture noise.
var fixtureDenyEnabled atomic.Bool

// EnableFixtureDeny turns on the agentsview-test-fixture deny-list for
// subsequent Scan and ScanDefinite calls. Wired into the CLI entrypoint so the
// long-running server, ad-hoc CLI commands, and sync engine all filter fixture
// noise. Off by default so unit tests can assert positive rule paths against
// the same values.
func EnableFixtureDeny() {
	fixtureDenyEnabled.Store(true)
}

// disableFixtureDenyForTest restores fixtureDenyEnabled to its previous value
// after the cleanup runs. Used by the secrets package own tests that want to
// exercise the deny-list path explicitly.
func disableFixtureDenyForTest(cleanup func(func())) {
	prev := fixtureDenyEnabled.Swap(false)
	cleanup(func() { fixtureDenyEnabled.Store(prev) })
}
