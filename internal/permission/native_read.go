package permission

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// LiveIngressContractVersion identifies the three-component asynchronous
	// ingress separation contract (XIN-2793 / XIN-2794 / XIN-2795).
	LiveIngressContractVersion = "v2_async_ingress_separation"
	// LiveIngressDeliveryPrefix identifies a live-turn delivery reference
	// bound to current channel/principal/space ACL without requiring the
	// asynchronous MySQL trigger message row.
	LiveIngressDeliveryPrefix = "l1-"
	// LiveIngressHeaderPrefix identifies the metadata-only NativeHeader token
	// corresponding to an l1- live-turn delivery reference.
	LiveIngressHeaderPrefix = "l2-"
	// LiveUnpersistedMessageVersion is the deterministic native_message_version
	// stamped on an l1-/l2- live turn when the asynchronous trigger row is not
	// yet present in MySQL.
	LiveUnpersistedMessageVersion = "live_unpersisted_v1"
	// LiveChannelACLProvenance attests that current Bot/caller/channel/space
	// access was evaluated under fenced SQL/IM authority cuts independently of
	// asynchronous message-row persistence.
	LiveChannelACLProvenance = "live_channel_acl_v1"
)

// CanonicalDMPairID computes the collision-safe canonical DM channel ID
// (<low>@<high> ordered by CRC32-IEEE) matching octo-server PairReference and
// codex-channel-octo canonicalDmPairReferenceId.
func CanonicalDMPairID(botUID, peerUID string) (string, error) {
	if !ValidNativeID(botUID) || !ValidNativeID(peerUID) || botUID == peerUID ||
		strings.Contains(botUID, "@") || strings.Contains(peerUID, "@") {
		return "", ErrInvalid
	}
	botCRC := crc32.ChecksumIEEE([]byte(botUID))
	peerCRC := crc32.ChecksumIEEE([]byte(peerUID))
	if botCRC == peerCRC {
		return "", ErrInvalid
	}
	var pair string
	if botCRC > peerCRC {
		pair = botUID + "@" + peerUID
	} else {
		pair = peerUID + "@" + botUID
	}
	if !validText(pair, 100, 400) {
		return "", ErrInvalid
	}
	return pair, nil
}

// LiveIngressReference holds the decoded, validated components of an l1- or l2-
// live-ingress reference token.
type LiveIngressReference struct {
	Prefix             string
	ChannelType        int
	SourceKind         string
	CanonicalChannelID string
	MessageID          string
	MessageSeq         string
	FromUID            string
	PayloadSHA256      string
}

// EncodeLiveIngressReference constructs a canonical l1- or l2- live-ingress token.
func EncodeLiveIngressReference(prefix string, channelType int, canonicalChannelID, messageID, messageSeq, fromUID, payloadSHA256 string) (string, error) {
	if prefix != "l1" && prefix != "l2" {
		return "", ErrInvalid
	}
	ref := prefix + "-" + strconv.Itoa(channelType) + "-" +
		hex.EncodeToString([]byte(canonicalChannelID)) + "-" +
		messageID + "-" + messageSeq + "-" +
		hex.EncodeToString([]byte(fromUID)) + "-" + payloadSHA256
	if _, err := ParseLiveIngressReference(prefix, ref); err != nil {
		return "", ErrInvalid
	}
	return ref, nil
}

// ParseLiveIngressReference parses and validates an l1- or l2- live-ingress
// reference token. Any malformed field, non-canonical hex encoding, or out-of-range
// sequence/message ID is rejected.
func ParseLiveIngressReference(expectedPrefix, ref string) (LiveIngressReference, error) {
	if !token(ref) || (expectedPrefix != "l1" && expectedPrefix != "l2") {
		return LiveIngressReference{}, ErrInvalid
	}
	parts := strings.Split(ref, "-")
	if len(parts) != 7 || parts[0] != expectedPrefix {
		return LiveIngressReference{}, ErrInvalid
	}
	var channelType int
	var sourceKind string
	switch parts[1] {
	case "1":
		channelType = 1
		sourceKind = "dm"
	case "2":
		channelType = 2
		sourceKind = "group"
	case "5":
		channelType = 5
		sourceKind = "thread"
	default:
		return LiveIngressReference{}, ErrInvalid
	}
	chBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(chBytes) == 0 || hex.EncodeToString(chBytes) != parts[2] {
		return LiveIngressReference{}, ErrInvalid
	}
	channelID := string(chBytes)
	switch channelType {
	case 1:
		if !validText(channelID, 100, 400) || !strings.Contains(channelID, "@") {
			return LiveIngressReference{}, ErrInvalid
		}
	case 2:
		if !ValidNativeID(channelID) {
			return LiveIngressReference{}, ErrInvalid
		}
	case 5:
		if !validText(channelID, 256, 256) {
			return LiveIngressReference{}, ErrInvalid
		}
	}
	msgID := parts[3]
	if msgID == "" || msgID[0] == '0' {
		return LiveIngressReference{}, ErrInvalid
	}
	if n, err := strconv.ParseInt(msgID, 10, 64); err != nil || n <= 0 || strconv.FormatInt(n, 10) != msgID {
		return LiveIngressReference{}, ErrInvalid
	}
	msgSeq := parts[4]
	if msgSeq == "" || msgSeq[0] == '0' {
		return LiveIngressReference{}, ErrInvalid
	}
	if seq, err := strconv.ParseUint(msgSeq, 10, 32); err != nil || seq == 0 || strconv.FormatUint(seq, 10) != msgSeq {
		return LiveIngressReference{}, ErrInvalid
	}
	fromBytes, err := hex.DecodeString(parts[5])
	if err != nil || len(fromBytes) == 0 || hex.EncodeToString(fromBytes) != parts[5] {
		return LiveIngressReference{}, ErrInvalid
	}
	fromUID := string(fromBytes)
	if !ValidNativeID(fromUID) {
		return LiveIngressReference{}, ErrInvalid
	}
	if !validHex(parts[6], 64) {
		return LiveIngressReference{}, ErrInvalid
	}
	return LiveIngressReference{
		Prefix:             expectedPrefix,
		ChannelType:        channelType,
		SourceKind:         sourceKind,
		CanonicalChannelID: channelID,
		MessageID:          msgID,
		MessageSeq:         msgSeq,
		FromUID:            fromUID,
		PayloadSHA256:      parts[6],
	}, nil
}

func sourceKindForChannelType(channelType int) (string, bool) {
	switch channelType {
	case 1:
		return "dm", true
	case 2:
		return "group", true
	case 5:
		return "thread", true
	default:
		return "", false
	}
}

func isLiveDeliveryReference(ref string) bool {
	return strings.HasPrefix(ref, LiveIngressDeliveryPrefix)
}

func isForbiddenDeliveryReferencePrefix(ref string) bool {
	return strings.HasPrefix(ref, LiveIngressHeaderPrefix) ||
		strings.HasPrefix(ref, "h1-") ||
		strings.HasPrefix(ref, "c1-")
}

// LiveIngressRequest carries the authenticated live-delivery observation for
// Wire Contract A (POST /v1/bot/permission-facts/v1/bind-delivery with
// operation="bind_live_ingress").
type LiveIngressRequest struct {
	ChannelType           int    `json:"channel_type"`
	ChannelID             string `json:"channel_id"`
	MessageID             string `json:"message_id"`
	MessageSeq            string `json:"message_seq"`
	FromUID               string `json:"from_uid"`
	ObservedPayloadSHA256 string `json:"observed_payload_sha256"`
}

// LiveIngressBinding is the verified server response from Wire Contract A
// (operation="bind_live_ingress").
type LiveIngressBinding struct {
	ContractVersion         string
	Provenance              string
	TriggerRowPersisted     bool
	ContentAttestedByServer bool
	DeliveryReference       string
	HeaderReference         string
	NativeMessageVersion    string
	ObservedPayloadSHA256   string
	Header                  NativeHeader
	ProvenPredicates        map[string]bool
	UnknownPredicates       []string
	ResolvablePurposes      []string
}

// CurrentAccessRequest carries the parameters for Wire Contract C
// (POST /v1/bot/permission-facts/v1 with operation="evaluate_current_access").
type CurrentAccessRequest struct {
	Purpose               string `json:"purpose"`
	ChannelType           int    `json:"channel_type"`
	ChannelID             string `json:"channel_id"`
	CallerUID             string `json:"caller_uid"`
	MessageID             string `json:"message_id,omitempty"`
	MessageSeq            string `json:"message_seq,omitempty"`
	ObservedPayloadSHA256 string `json:"observed_payload_sha256,omitempty"`
}

// CurrentAccessFact is the verified server response from Wire Contract C
// (operation="evaluate_current_access").
type CurrentAccessFact struct {
	ContractVersion         string
	Provenance              string
	TriggerRowPersisted     bool
	ContentAttestedByServer bool
	Purpose                 string
	Subject                 Subject
	AuthorKind              string
	Profile                 string
	Eligible                bool
	Predicates              map[string]bool
}

//go:embed contracts/shared-contract-v6.json
var nativeSharedContract []byte

// NativeHeader is the unchanged metadata-only native Header wire record.
type NativeHeader struct {
	Version   int    `json:"header_version"`
	Reference string `json:"header_reference"`
	MessageID string `json:"message_id"`
	Source    struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"source"`
	Parent         *string `json:"parent_group"`
	SpaceID        string  `json:"space_id"`
	AuthorUID      string  `json:"author_uid"`
	AuthorKind     string  `json:"author_kind"`
	BotUID         string  `json:"recipient_bot_uid"`
	MessageVersion string  `json:"native_message_version"`
	PayloadSHA256  string  `json:"payload_sha256"`
	Delivery       struct {
		StreamID    string `json:"stream_id"`
		Incarnation string `json:"stream_incarnation"`
		Sequence    string `json:"sequence"`
	} `json:"delivery"`
}

// AuthorityPin identifies a native authority whose currentness/fencing contract
// is configured by the trusted host, never learned from an untrusted response.
type AuthorityPin struct {
	Kind        string `json:"kind"`
	Incarnation string `json:"incarnation"`
}

// Observation cannot be minted by a declaration: only an authenticated native
// response constructs its private reader/key/reference provenance.
type Observation struct {
	header      NativeHeader
	raw         []byte
	reader      *NativeReader
	key         Key
	reference   string
	eligible    bool
	liveIngress bool
}

// Header returns a detached metadata copy for private host correlation.
func (o *Observation) Header() NativeHeader {
	h := o.header
	if h.Parent != nil {
		parent := *h.Parent
		h.Parent = &parent
	}
	return h
}

// IsLiveIngress reports whether the observation was minted from an unpersisted
// live WS ingress token (l1-/l2-, live_unpersisted_v1).
func (o *Observation) IsLiveIngress() bool {
	return o.liveIngress || isLiveDeliveryReference(o.reference) || strings.HasPrefix(o.header.Reference, LiveIngressHeaderPrefix) || o.header.MessageVersion == LiveUnpersistedMessageVersion
}

type nativeGeneration struct {
	key          Key
	original     Observation
	contributors []Observation
	bound        bool
}

const (
	// MaxNativeReads caps retained read_id and authority read_nonce replay fences.
	// Without a proven native horizon, saturation fails closed rather than evicting fences.
	MaxNativeReads = 100000
	// MaxNativeObservations caps retained cross-read HeaderV1 immutability records.
	MaxNativeObservations = 100000
	// MaxNativeGenerations caps concurrently active generation bindings.
	MaxNativeGenerations = 1024
)

// NativeReader consumes the existing exact-Bot native-facts contract. It does
// not provide the server's native authority, disclosure or checkpoint methods.
type NativeReader struct {
	self            *SelfPolicyReader
	pins            map[string]AuthorityPin
	profiles        map[string][]string
	mu              sync.Mutex
	reads           map[string]bool
	generations     map[string]nativeGeneration
	observations    map[headerKey]Observation
	maxReads        int
	maxObservations int
	maxGenerations  int
}
type headerKey struct {
	Key
	reference string
}

func (r *NativeReader) readLimit() int {
	if r.maxReads > 0 {
		return r.maxReads
	}
	return MaxNativeReads
}

func (r *NativeReader) observationLimit() int {
	if r.maxObservations > 0 {
		return r.maxObservations
	}
	return MaxNativeObservations
}

func (r *NativeReader) generationLimit() int {
	if r.maxGenerations > 0 {
		return r.maxGenerations
	}
	return MaxNativeGenerations
}

// NewNativeReader fixes authority pins and the exact unchanged predicate table.
func NewNativeReader(self *SelfPolicyReader, pins map[string]AuthorityPin) (*NativeReader, error) {
	if self == nil || len(pins) == 0 {
		return nil, ErrUnavailable
	}
	copyPins := map[string]AuthorityPin{}
	for id, p := range pins {
		if !asciiToken(id, 64, false) || (p.Kind != "sql" && p.Kind != "im") || !validHex(p.Incarnation, 32) {
			return nil, ErrInvalid
		}
		copyPins[id] = p
	}
	var shared struct {
		Profiles map[string][]string `json:"predicate_profiles"`
	}
	if json.Unmarshal(nativeSharedContract, &shared) != nil || len(shared.Profiles) != 10 {
		return nil, ErrUnavailable
	}
	return &NativeReader{self: self, pins: copyPins, profiles: shared.Profiles, reads: map[string]bool{}, generations: map[string]nativeGeneration{}, observations: map[headerKey]Observation{}}, nil
}

func token(s string) bool {
	if s == "" || len(s) > 512 {
		return false
	}
	for _, c := range s {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !valid {
			return false
		}
	}
	return true
}
func utcTimestamp(v any) bool {
	s, ok := v.(string)
	if !ok || !strings.HasSuffix(s, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func parseNativeHeader(v any) (NativeHeader, []byte, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	m := object(v)
	if !fields(m, "header_version", "header_reference", "message_id", "source", "parent_group", "space_id", "author_uid", "author_kind", "recipient_bot_uid", "native_message_version", "payload_sha256", "delivery") || m["header_version"] != json.Number("1") {
		return NativeHeader{}, nil, ErrUnavailable
	}
	if !token(text(m["header_reference"])) || !token(text(m["native_message_version"])) || !validHex(text(m["payload_sha256"]), 64) {
		return NativeHeader{}, nil, ErrUnavailable
	}
	var h NativeHeader
	raw := canonical(m)
	if json.Unmarshal(raw, &h) != nil {
		return h, nil, ErrUnavailable
	}
	channelType := 1
	switch h.Source.Kind {
	case "dm":
	case "group":
		channelType = 2
	case "thread":
		channelType = 5
	default:
		return h, nil, ErrUnavailable
	}
	decl, err := json.Marshal(map[string]any{"version": 2, "im": map[string]any{"sender": h.AuthorUID, "channel": h.Source.ID, "channel_type": channelType, "space_id": h.SpaceID, "message_id": h.MessageID}})
	if err != nil {
		return h, nil, ErrUnavailable
	}
	if _, err := ParseDeclarationV2(decl); err != nil || !ValidNativeID(h.BotUID) {
		return h, nil, ErrUnavailable
	}
	if !fields(object(m["source"]), "kind", "id") || !fields(object(m["delivery"]), "stream_id", "stream_incarnation", "sequence") || !asciiToken(h.Delivery.StreamID, 128, false) || !validHex(h.Delivery.Incarnation, 32) {
		return h, nil, ErrUnavailable
	}
	if _, err := U64(h.Delivery.Sequence); err != nil {
		return h, nil, ErrUnavailable
	}
	if h.Source.Kind == "thread" {
		if h.Parent == nil || !ValidNativeID(*h.Parent) {
			return h, nil, ErrUnavailable
		}
	} else if h.Parent != nil {
		return h, nil, ErrUnavailable
	}
	if h.AuthorKind != "human" && h.AuthorKind != "user_bot" && h.AuthorKind != "app_bot" && h.AuthorKind != "system" {
		return h, nil, ErrUnavailable
	}
	return h, raw, nil
}

// Resolve performs one fixed authenticated metadata request; no body/history
// retrieval or implicit lookup by a supplied author/conversation is possible.
func (r *NativeReader) Resolve(ctx context.Context, key Key, purpose string, references []string) ([]Observation, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if key.Origin != r.self.origin || key.BotUID != r.self.botUID || !key.Valid() || len(references) < 1 || len(references) > 32 || (purpose != "admit" && purpose != "contributor" && purpose != "reply") {
		return nil, ErrUnavailable
	}
	refs := make([]map[string]string, len(references))
	parsedLive := make([]*LiveIngressReference, len(references))
	seen := map[string]bool{}
	for i, ref := range references {
		if !token(ref) || seen[ref] || isForbiddenDeliveryReferencePrefix(ref) {
			return nil, ErrUnavailable
		}
		if isLiveDeliveryReference(ref) {
			parsed, err := ParseLiveIngressReference("l1", ref)
			if err != nil {
				return nil, ErrUnavailable
			}
			parsedLive[i] = &parsed
		}
		seen[ref] = true
		refs[i] = map[string]string{"delivery_reference": ref}
	}
	id := randomID()
	body, err := json.Marshal(map[string]any{"schema_version": 1, "profile_version": 1, "request_id": id, "operation": "resolve", "purpose": purpose, "references": refs})
	if err != nil || len(body) > 32768 {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, key.Origin+"/v1/bot/permission-facts/v1?"+url.Values{"space_id": {key.SpaceID}}.Encode(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+r.self.token)
	req.Header.Set("X-Space-ID", key.SpaceID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store")
	resp, err := r.self.client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer closeOwned(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" {
		return nil, ErrUnavailable
	}
	v, err := decodeStrict(io.LimitReader(resp.Body, MaxBundleBytes+1), MaxBundleBytes)
	if err != nil {
		return nil, ErrUnavailable
	}
	m := object(v)
	if !fields(m, "schema_version", "profile_version", "key", "request_id", "read_id", "observed_at", "authorities", "results") || m["schema_version"] != json.Number("1") || m["profile_version"] != json.Number("1") || m["request_id"] != id || !validHex(text(m["read_id"]), 32) || !utcTimestamp(m["observed_at"]) {
		return nil, ErrUnavailable
	}
	var actual Key
	if json.Unmarshal(canonical(m["key"]), &actual) != nil || actual != key || !fields(object(m["key"]), "origin", "bot_uid", "space_id") {
		return nil, ErrUnavailable
	}
	authorities, ok := items(m["authorities"], len(r.pins), len(r.pins))
	if !ok {
		return nil, ErrUnavailable
	}
	nonces := map[string]bool{}
	last := ""
	for _, v := range authorities {
		a := object(v)
		id := text(a["authority_id"])
		pin, ok := r.pins[id]
		if !ok || id <= last || !fields(a, "authority_id", "kind", "incarnation", "position", "read_nonce", "consistency", "started_at", "completed_at") || a["kind"] != pin.Kind || a["incarnation"] != pin.Incarnation || a["consistency"] != "ack_before_read_start" || !token(text(a["position"])) || !validHex(text(a["read_nonce"]), 32) || !utcTimestamp(a["started_at"]) || !utcTimestamp(a["completed_at"]) {
			return nil, ErrUnavailable
		}
		last = id
		nonce := text(a["read_nonce"])
		if nonces[nonce] {
			return nil, ErrUnavailable
		}
		nonces[nonce] = true
	}
	results, ok := items(m["results"], len(references), len(references))
	if !ok {
		return nil, ErrUnavailable
	}
	observations := make([]Observation, len(results))
	for i, v := range results {
		a := object(v)
		if len(canonical(a)) > 8192 || a["reference"] != references[i] || a["status"] != "resolved" {
			return nil, ErrUnavailable
		}
		if !resolveResultFieldsValid(a, parsedLive[i] != nil) {
			return nil, ErrUnavailable
		}
		h, raw, err := parseNativeHeader(a["header"])
		if err != nil || h.BotUID != key.BotUID || h.SpaceID != key.SpaceID {
			return nil, ErrUnavailable
		}
		if parsed := parsedLive[i]; parsed != nil {
			expectedHeaderRef := LiveIngressHeaderPrefix + strings.TrimPrefix(references[i], LiveIngressDeliveryPrefix)
			if h.Reference != expectedHeaderRef || h.Source.Kind != parsed.SourceKind || h.Source.ID != parsed.CanonicalChannelID || h.MessageID != parsed.MessageID || h.Delivery.Sequence != parsed.MessageSeq || h.AuthorUID != parsed.FromUID || h.PayloadSHA256 != parsed.PayloadSHA256 {
				return nil, ErrUnavailable
			}
			if h.Source.Kind == "dm" {
				canonicalPair, err := CanonicalDMPairID(key.BotUID, h.AuthorUID)
				if err != nil || h.Source.ID != canonicalPair {
					return nil, ErrUnavailable
				}
			}
			if purpose == "contributor" {
				return nil, ErrUnavailable
			}
			if h.MessageVersion == LiveUnpersistedMessageVersion {
				if attested, hasAttested := a["content_attested_by_server"].(bool); hasAttested && attested {
					return nil, ErrUnavailable
				}
			}
		} else if strings.HasPrefix(h.Reference, LiveIngressHeaderPrefix) || h.MessageVersion == LiveUnpersistedMessageVersion {
			return nil, ErrUnavailable
		}
		profile := h.Source.Kind + "_" + purpose + "_v1"
		if h.AuthorKind != "human" {
			profile = "unsupported_principal_v1"
		}
		if a["profile"] != profile {
			return nil, ErrUnavailable
		}
		atoms := r.profiles[profile]
		predicates := object(a["predicates"])
		if len(atoms) == 0 || !fields(predicates, atoms...) {
			return nil, ErrUnavailable
		}
		eligible := true
		for _, atom := range atoms {
			value, ok := predicates[atom].(bool)
			if !ok {
				return nil, ErrUnavailable
			}
			eligible = eligible && value
		}
		if profile == "unsupported_principal_v1" && (predicates["identity_supported"] != false || predicates["author_is_human"] != false) {
			return nil, ErrUnavailable
		}
		used, ok := items(a["authority_refs"], 1, len(nonces))
		if !ok {
			return nil, ErrUnavailable
		}
		unique := map[string]bool{}
		for _, v := range used {
			n := text(v)
			if !nonces[n] || unique[n] {
				return nil, ErrUnavailable
			}
			unique[n] = true
		}
		observations[i] = Observation{header: h, raw: raw, reader: r, key: key, reference: references[i], eligible: eligible, liveIngress: parsedLive[i] != nil}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	readID := "read:" + text(m["read_id"])
	if r.reads[readID] {
		return nil, ErrUnavailable
	}
	for n := range nonces {
		if r.reads[n] {
			return nil, ErrUnavailable
		}
	}
	newObservations := map[headerKey]bool{}
	for j := range observations {
		o := &observations[j]
		k := headerKey{o.key, o.header.Reference}
		if prior, ok := r.observations[k]; ok {
			if !bytes.Equal(prior.raw, o.raw) {
				return nil, ErrUnavailable
			}
		} else {
			newObservations[k] = true
		}
	}
	if len(r.reads)+1+len(nonces) > r.readLimit() || len(r.observations)+len(newObservations) > r.observationLimit() {
		return nil, ErrUnavailable
	}
	for n := range nonces {
		r.reads[n] = true
	}
	r.reads[readID] = true
	for j := range observations {
		o := &observations[j]
		k := headerKey{o.key, o.header.Reference}
		r.observations[k] = *o
	}
	return observations, nil
}

func resolveResultFieldsValid(a map[string]any, isLive bool) bool {
	if fields(a, "reference", "status", "header", "profile", "authority_refs", "predicates") {
		return true
	}
	if !isLive || !fields(a, "reference", "status", "provenance", "trigger_row_persisted", "content_attested_by_server", "header", "profile", "authority_refs", "predicates") {
		return false
	}
	if text(a["provenance"]) != LiveChannelACLProvenance {
		return false
	}
	persisted, ok1 := a["trigger_row_persisted"].(bool)
	attested, ok2 := a["content_attested_by_server"].(bool)
	if !ok1 || !ok2 {
		return false
	}
	if !persisted && attested {
		return false
	}
	return true
}

// BindLiveIngress executes Wire Contract A (POST /v1/bot/permission-facts/v1/bind-delivery
// with operation "bind_live_ingress") to canonicalize a live WS ingress event,
// evaluate current SQL Bot/caller/channel/space ACL under fenced authority cuts
// without requiring the trigger row in MySQL, and mint an l1-/l2- reference pair.
func (r *NativeReader) BindLiveIngress(ctx context.Context, key Key, req LiveIngressRequest) (LiveIngressBinding, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if key.Origin != r.self.origin || key.BotUID != r.self.botUID || !key.Valid() {
		return LiveIngressBinding{}, ErrUnavailable
	}
	if (req.ChannelType != 1 && req.ChannelType != 2 && req.ChannelType != 5) || !ValidNativeID(req.ChannelID) || !ValidNativeID(req.FromUID) {
		return LiveIngressBinding{}, ErrUnavailable
	}
	if _, err := U64(req.MessageID); err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	if _, err := U64(req.MessageSeq); err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	if !validHex(req.ObservedPayloadSHA256, 64) {
		return LiveIngressBinding{}, ErrUnavailable
	}
	id := randomID()
	body, err := json.Marshal(map[string]any{
		"schema_version":  1,
		"profile_version": 1,
		"request_id":      id,
		"operation":       "bind_live_ingress",
		"live_ingress": map[string]any{
			"channel_type":            req.ChannelType,
			"channel_id":              req.ChannelID,
			"message_id":              req.MessageID,
			"message_seq":             req.MessageSeq,
			"from_uid":                req.FromUID,
			"observed_payload_sha256": req.ObservedPayloadSHA256,
		},
	})
	if err != nil || len(body) > 32768 {
		return LiveIngressBinding{}, ErrUnavailable
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, key.Origin+"/v1/bot/permission-facts/v1/bind-delivery?"+url.Values{"space_id": {key.SpaceID}}.Encode(), bytes.NewReader(body))
	if err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.self.token)
	httpReq.Header.Set("X-Space-ID", key.SpaceID)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Cache-Control", "no-store")
	resp, err := r.self.client.Do(httpReq)
	if err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	defer closeOwned(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" {
		return LiveIngressBinding{}, ErrUnavailable
	}
	v, err := decodeStrict(io.LimitReader(resp.Body, MaxBundleBytes+1), MaxBundleBytes)
	if err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	m := object(v)
	if !fields(m, "schema_version", "profile_version", "key", "request_id", "read_id", "observed_at", "authorities", "result") || m["schema_version"] != json.Number("1") || m["profile_version"] != json.Number("1") || m["request_id"] != id || !validHex(text(m["read_id"]), 32) || !utcTimestamp(m["observed_at"]) {
		return LiveIngressBinding{}, ErrUnavailable
	}
	var actual Key
	if json.Unmarshal(canonical(m["key"]), &actual) != nil || actual != key || !fields(object(m["key"]), "origin", "bot_uid", "space_id") {
		return LiveIngressBinding{}, ErrUnavailable
	}
	authorities, ok := items(m["authorities"], len(r.pins), len(r.pins))
	if !ok {
		return LiveIngressBinding{}, ErrUnavailable
	}
	nonces := map[string]bool{}
	last := ""
	for _, v := range authorities {
		a := object(v)
		aid := text(a["authority_id"])
		pin, ok := r.pins[aid]
		if !ok || aid <= last || !fields(a, "authority_id", "kind", "incarnation", "position", "read_nonce", "consistency", "started_at", "completed_at") || a["kind"] != pin.Kind || a["incarnation"] != pin.Incarnation || a["consistency"] != "ack_before_read_start" || !token(text(a["position"])) || !validHex(text(a["read_nonce"]), 32) || !utcTimestamp(a["started_at"]) || !utcTimestamp(a["completed_at"]) {
			return LiveIngressBinding{}, ErrUnavailable
		}
		last = aid
		nonce := text(a["read_nonce"])
		if nonces[nonce] {
			return LiveIngressBinding{}, ErrUnavailable
		}
		nonces[nonce] = true
	}
	res := object(m["result"])
	if !fields(res, "status", "contract_version", "provenance", "trigger_row_persisted", "content_attested_by_server", "delivery_reference", "header_reference", "native_message_version", "source", "parent_group", "author_uid", "author_kind", "recipient_bot_uid", "space_id", "message_id", "payload_sha256", "delivery", "resolvable_purposes") || res["status"] != "resolved" || text(res["contract_version"]) != LiveIngressContractVersion || text(res["provenance"]) != LiveChannelACLProvenance {
		return LiveIngressBinding{}, ErrUnavailable
	}
	persisted, ok1 := res["trigger_row_persisted"].(bool)
	attested, ok2 := res["content_attested_by_server"].(bool)
	if !ok1 || !ok2 || (!persisted && attested) {
		return LiveIngressBinding{}, ErrUnavailable
	}
	deliveryRef := text(res["delivery_reference"])
	headerRef := text(res["header_reference"])
	parsedDelivery, err := ParseLiveIngressReference("l1", deliveryRef)
	if err != nil {
		return LiveIngressBinding{}, ErrUnavailable
	}
	if headerRef != LiveIngressHeaderPrefix+strings.TrimPrefix(deliveryRef, LiveIngressDeliveryPrefix) {
		return LiveIngressBinding{}, ErrUnavailable
	}
	expectedChannelID := req.ChannelID
	if req.ChannelType == 1 {
		expectedChannelID, err = CanonicalDMPairID(key.BotUID, req.FromUID)
		if err != nil {
			return LiveIngressBinding{}, ErrUnavailable
		}
	}
	if parsedDelivery.ChannelType != req.ChannelType || parsedDelivery.CanonicalChannelID != expectedChannelID || parsedDelivery.MessageID != req.MessageID || parsedDelivery.MessageSeq != req.MessageSeq || parsedDelivery.FromUID != req.FromUID || parsedDelivery.PayloadSHA256 != req.ObservedPayloadSHA256 {
		return LiveIngressBinding{}, ErrUnavailable
	}
	headerMap := map[string]any{
		"header_version":         json.Number("1"),
		"header_reference":       res["header_reference"],
		"message_id":             res["message_id"],
		"source":                 res["source"],
		"parent_group":           res["parent_group"],
		"space_id":               res["space_id"],
		"author_uid":             res["author_uid"],
		"author_kind":            res["author_kind"],
		"recipient_bot_uid":      res["recipient_bot_uid"],
		"native_message_version": res["native_message_version"],
		"payload_sha256":         res["payload_sha256"],
		"delivery":               res["delivery"],
	}
	h, _, err := parseNativeHeader(headerMap)
	if err != nil || h.BotUID != key.BotUID || h.SpaceID != key.SpaceID || h.Source.Kind != parsedDelivery.SourceKind || h.Source.ID != expectedChannelID || h.AuthorUID != req.FromUID || h.MessageID != req.MessageID || h.Delivery.Sequence != req.MessageSeq || h.PayloadSHA256 != req.ObservedPayloadSHA256 || h.MessageVersion != LiveUnpersistedMessageVersion {
		return LiveIngressBinding{}, ErrUnavailable
	}
	purposesRaw, ok := items(res["resolvable_purposes"], 1, 2)
	if !ok {
		return LiveIngressBinding{}, ErrUnavailable
	}
	purposes := make([]string, 0, len(purposesRaw))
	for _, pv := range purposesRaw {
		ps := text(pv)
		if ps != "admit" && ps != "reply" {
			return LiveIngressBinding{}, ErrUnavailable
		}
		purposes = append(purposes, ps)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	readID := "read:" + text(m["read_id"])
	if r.reads[readID] {
		return LiveIngressBinding{}, ErrUnavailable
	}
	for n := range nonces {
		if r.reads[n] {
			return LiveIngressBinding{}, ErrUnavailable
		}
	}
	if len(r.reads)+1+len(nonces) > r.readLimit() {
		return LiveIngressBinding{}, ErrUnavailable
	}
	for n := range nonces {
		r.reads[n] = true
	}
	r.reads[readID] = true
	return LiveIngressBinding{
		ContractVersion:         LiveIngressContractVersion,
		Provenance:              LiveChannelACLProvenance,
		TriggerRowPersisted:     persisted,
		ContentAttestedByServer: attested,
		DeliveryReference:       deliveryRef,
		HeaderReference:         headerRef,
		NativeMessageVersion:    h.MessageVersion,
		Header:                  h,
		ResolvablePurposes:      purposes,
	}, nil
}

// EvaluateCurrentAccess executes Wire Contract C (POST /v1/bot/permission-facts/v1
// with operation "evaluate_current_access") for direct stateless current-access
// verification without requiring the trigger message row in MySQL.
func (r *NativeReader) EvaluateCurrentAccess(ctx context.Context, key Key, req CurrentAccessRequest) (CurrentAccessFact, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if key.Origin != r.self.origin || key.BotUID != r.self.botUID || !key.Valid() || (req.Purpose != "admit" && req.Purpose != "reply") {
		return CurrentAccessFact{}, ErrUnavailable
	}
	if (req.ChannelType != 1 && req.ChannelType != 2 && req.ChannelType != 5) || !ValidNativeID(req.ChannelID) || !ValidNativeID(req.CallerUID) {
		return CurrentAccessFact{}, ErrUnavailable
	}
	accessPayload := map[string]any{
		"channel_type": req.ChannelType,
		"channel_id":   req.ChannelID,
		"caller_uid":   req.CallerUID,
	}
	if req.MessageID != "" || req.MessageSeq != "" || req.ObservedPayloadSHA256 != "" {
		if _, err := U64(req.MessageID); err != nil {
			return CurrentAccessFact{}, ErrUnavailable
		}
		if _, err := U64(req.MessageSeq); err != nil {
			return CurrentAccessFact{}, ErrUnavailable
		}
		if !validHex(req.ObservedPayloadSHA256, 64) {
			return CurrentAccessFact{}, ErrUnavailable
		}
		accessPayload["message_id"] = req.MessageID
		accessPayload["message_seq"] = req.MessageSeq
		accessPayload["observed_payload_sha256"] = req.ObservedPayloadSHA256
	}
	id := randomID()
	body, err := json.Marshal(map[string]any{
		"schema_version":  1,
		"profile_version": 1,
		"request_id":      id,
		"operation":       "evaluate_current_access",
		"purpose":         req.Purpose,
		"current_access":  accessPayload,
	})
	if err != nil || len(body) > 32768 {
		return CurrentAccessFact{}, ErrUnavailable
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, key.Origin+"/v1/bot/permission-facts/v1?"+url.Values{"space_id": {key.SpaceID}}.Encode(), bytes.NewReader(body))
	if err != nil {
		return CurrentAccessFact{}, ErrUnavailable
	}
	httpReq.Header.Set("Authorization", "Bearer "+r.self.token)
	httpReq.Header.Set("X-Space-ID", key.SpaceID)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Cache-Control", "no-store")
	resp, err := r.self.client.Do(httpReq)
	if err != nil {
		return CurrentAccessFact{}, ErrUnavailable
	}
	defer closeOwned(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" {
		return CurrentAccessFact{}, ErrUnavailable
	}
	v, err := decodeStrict(io.LimitReader(resp.Body, MaxBundleBytes+1), MaxBundleBytes)
	if err != nil {
		return CurrentAccessFact{}, ErrUnavailable
	}
	m := object(v)
	if !fields(m, "schema_version", "profile_version", "key", "request_id", "read_id", "observed_at", "authorities", "result") || m["schema_version"] != json.Number("1") || m["profile_version"] != json.Number("1") || m["request_id"] != id || !validHex(text(m["read_id"]), 32) || !utcTimestamp(m["observed_at"]) {
		return CurrentAccessFact{}, ErrUnavailable
	}
	var actual Key
	if json.Unmarshal(canonical(m["key"]), &actual) != nil || actual != key || !fields(object(m["key"]), "origin", "bot_uid", "space_id") {
		return CurrentAccessFact{}, ErrUnavailable
	}
	authorities, ok := items(m["authorities"], len(r.pins), len(r.pins))
	if !ok {
		return CurrentAccessFact{}, ErrUnavailable
	}
	nonces := map[string]bool{}
	last := ""
	for _, v := range authorities {
		a := object(v)
		aid := text(a["authority_id"])
		pin, ok := r.pins[aid]
		if !ok || aid <= last || !fields(a, "authority_id", "kind", "incarnation", "position", "read_nonce", "consistency", "started_at", "completed_at") || a["kind"] != pin.Kind || a["incarnation"] != pin.Incarnation || a["consistency"] != "ack_before_read_start" || !token(text(a["position"])) || !validHex(text(a["read_nonce"]), 32) || !utcTimestamp(a["started_at"]) || !utcTimestamp(a["completed_at"]) {
			return CurrentAccessFact{}, ErrUnavailable
		}
		last = aid
		nonce := text(a["read_nonce"])
		if nonces[nonce] {
			return CurrentAccessFact{}, ErrUnavailable
		}
		nonces[nonce] = true
	}
	res := object(m["result"])
	if !fields(res, "status", "contract_version", "provenance", "trigger_row_persisted", "content_attested_by_server", "purpose", "source", "parent_group", "space_id", "author_uid", "author_kind", "recipient_bot_uid", "profile", "authority_refs", "predicates") || res["status"] != "resolved" || text(res["contract_version"]) != LiveIngressContractVersion || text(res["provenance"]) != LiveChannelACLProvenance || text(res["purpose"]) != req.Purpose || text(res["space_id"]) != key.SpaceID || text(res["recipient_bot_uid"]) != key.BotUID || text(res["author_uid"]) != req.CallerUID {
		return CurrentAccessFact{}, ErrUnavailable
	}
	persisted, ok1 := res["trigger_row_persisted"].(bool)
	attested, ok2 := res["content_attested_by_server"].(bool)
	if !ok1 || !ok2 || (!persisted && attested) {
		return CurrentAccessFact{}, ErrUnavailable
	}
	src := object(res["source"])
	if !fields(src, "kind", "id") {
		return CurrentAccessFact{}, ErrUnavailable
	}
	sourceKind := text(src["kind"])
	sourceID := text(src["id"])
	expectedKind, ok := sourceKindForChannelType(req.ChannelType)
	if !ok || sourceKind != expectedKind {
		return CurrentAccessFact{}, ErrUnavailable
	}
	expectedSourceID := req.ChannelID
	if req.ChannelType == 1 {
		expectedSourceID, err = CanonicalDMPairID(key.BotUID, req.CallerUID)
		if err != nil {
			return CurrentAccessFact{}, ErrUnavailable
		}
	}
	if sourceID != expectedSourceID {
		return CurrentAccessFact{}, ErrUnavailable
	}
	parentGroup := ""
	if sourceKind == "thread" {
		parentGroup = text(res["parent_group"])
		if !ValidNativeID(parentGroup) {
			return CurrentAccessFact{}, ErrUnavailable
		}
	} else if res["parent_group"] != nil {
		return CurrentAccessFact{}, ErrUnavailable
	}
	authorKind := text(res["author_kind"])
	if authorKind != "human" && authorKind != "user_bot" && authorKind != "app_bot" && authorKind != "system" {
		return CurrentAccessFact{}, ErrUnavailable
	}
	profile := sourceKind + "_" + req.Purpose + "_v1"
	if authorKind != "human" {
		profile = "unsupported_principal_v1"
	}
	if text(res["profile"]) != profile {
		return CurrentAccessFact{}, ErrUnavailable
	}
	atoms := r.profiles[profile]
	predicatesObj := object(res["predicates"])
	if len(atoms) == 0 || !fields(predicatesObj, atoms...) {
		return CurrentAccessFact{}, ErrUnavailable
	}
	predicates := make(map[string]bool, len(atoms))
	eligible := true
	for _, atom := range atoms {
		value, ok := predicatesObj[atom].(bool)
		if !ok {
			return CurrentAccessFact{}, ErrUnavailable
		}
		predicates[atom] = value
		eligible = eligible && value
	}
	if profile == "unsupported_principal_v1" && (predicates["identity_supported"] || predicates["author_is_human"]) {
		return CurrentAccessFact{}, ErrUnavailable
	}
	used, ok := items(res["authority_refs"], 1, len(nonces))
	if !ok {
		return CurrentAccessFact{}, ErrUnavailable
	}
	unique := map[string]bool{}
	for _, v := range used {
		n := text(v)
		if !nonces[n] || unique[n] {
			return CurrentAccessFact{}, ErrUnavailable
		}
		unique[n] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	readID := "read:" + text(m["read_id"])
	if r.reads[readID] {
		return CurrentAccessFact{}, ErrUnavailable
	}
	for n := range nonces {
		if r.reads[n] {
			return CurrentAccessFact{}, ErrUnavailable
		}
	}
	if len(r.reads)+1+len(nonces) > r.readLimit() {
		return CurrentAccessFact{}, ErrUnavailable
	}
	for n := range nonces {
		r.reads[n] = true
	}
	r.reads[readID] = true
	return CurrentAccessFact{
		ContractVersion:         LiveIngressContractVersion,
		Provenance:              LiveChannelACLProvenance,
		TriggerRowPersisted:     persisted,
		ContentAttestedByServer: attested,
		Purpose:                 req.Purpose,
		Subject: Subject{
			Caller: req.CallerUID,
			Kind:   sourceKind,
			Source: sourceID,
			Parent: parentGroup,
		},
		AuthorKind: authorKind,
		Profile:    profile,
		Eligible:   eligible,
		Predicates: predicates,
	}, nil
}

// ConfigureBounds overrides the fail-closed retention ceilings before any reads
// or generations are recorded. It never enables sliding-window or TTL eviction.
func (r *NativeReader) ConfigureBounds(maxReads, maxObservations, maxGenerations int) error {
	if maxReads < 1 || maxReads > MaxNativeReads || maxObservations < 1 || maxObservations > MaxNativeObservations || maxGenerations < 1 || maxGenerations > MaxNativeGenerations {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reads) != 0 || len(r.observations) != 0 || len(r.generations) != 0 {
		return ErrConflict
	}
	r.maxReads = maxReads
	r.maxObservations = maxObservations
	r.maxGenerations = maxGenerations
	return nil
}

// ObservedHeader joins an endpoint's exact Header to a native-issued delivery
// reference previously resolved by this host. Header and delivery tokens are
// never treated as interchangeable. Unseen historical headers need the actual
// native reference bridge; a declaration cannot manufacture that proof.
func (r *NativeReader) ObservedHeader(key Key, value any) (Observation, error) {
	h, raw, err := parseNativeHeader(value)
	if err != nil {
		return Observation{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.observations[headerKey{key, h.Reference}]
	if !ok || !bytes.Equal(o.raw, raw) {
		return Observation{}, ErrUnavailable
	}
	return o, nil
}

// BindGeneration records the complete host-owned original union before relay
// access. A record from another reader/key cannot supply contributor authority.
func (r *NativeReader) BindGeneration(generation string, original Observation, contributors []Observation) error { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	if original.reader != r || len(contributors) > 256 || !validHex(generation, 32) {
		return ErrUnavailable
	}
	copyContributors := append([]Observation(nil), contributors...)
	for oi := range copyContributors {
		o := &copyContributors[oi]
		if o.reader != r || o.key != original.key {
			return ErrUnavailable
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.generations[generation]; exists {
		return ErrConflict
	}
	if len(r.generations) >= r.generationLimit() {
		return ErrUnavailable
	}
	r.generations[generation] = nativeGeneration{key: original.key, original: original, contributors: copyContributors}
	return nil
}

// ForgetGeneration erases private handles; it never rewinds native replay fences.
func (r *NativeReader) ForgetGeneration(generation string) {
	r.mu.Lock()
	delete(r.generations, generation)
	r.mu.Unlock()
}

// Recheck resolves original/current facts and every contributing original anew.
// Live-turn trigger observations (already resolved via purpose "admit" above)
// are reused directly rather than re-queried with purpose "contributor", while
// distinct prior live-turn originals in the same generation resolve via "admit"
// and historical stored contributors (s1-...) resolve via "contributor".
func (r *NativeReader) Recheck(ctx context.Context, b Binding, needs Needs) (Facts, error) { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	r.mu.Lock()
	g, ok := r.generations[b.Generation]
	r.mu.Unlock()
	if !ok || g.key != b.Key {
		return Facts{}, ErrUnavailable
	}
	current, err := r.Resolve(ctx, b.Key, "admit", []string{g.original.reference})
	if err != nil {
		return Facts{}, err
	}
	if !bytes.Equal(current[0].raw, g.original.raw) {
		return Facts{}, ContextUntrusted()
	}
	h := current[0].header
	subject := Subject{Caller: h.AuthorUID, Kind: h.Source.Kind, Source: h.Source.ID}
	if h.Parent != nil {
		subject.Parent = *h.Parent
	}
	f := Facts{Subject: subject, Complete: true, InvocationEligible: current[0].eligible, CapabilityEligible: current[0].eligible}
	if needs.Invocation && len(g.contributors) > 0 {
		out := make([]ContributorFact, len(g.contributors))
		var liveIndices []int
		var storedIndices []int
		for idx := range g.contributors {
			cObs := &g.contributors[idx]
			if cObs.reference == g.original.reference || cObs.header.Reference == g.original.header.Reference {
				if !bytes.Equal(cObs.raw, g.original.raw) {
					return Facts{}, ContextUntrusted()
				}
				out[idx] = ContributorFact{Subject: subject, Eligible: current[0].eligible}
				continue
			}
			if cObs.IsLiveIngress() {
				liveIndices = append(liveIndices, idx)
			} else {
				storedIndices = append(storedIndices, idx)
			}
		}
		if err := r.resolveContributorSubset(ctx, b.Key, "admit", g.contributors, liveIndices, out); err != nil {
			return Facts{}, err
		}
		if err := r.resolveContributorSubset(ctx, b.Key, "contributor", g.contributors, storedIndices, out); err != nil {
			return Facts{}, err
		}
		f.Contributors = out
	}
	return f, nil
}

func (r *NativeReader) resolveContributorSubset(ctx context.Context, key Key, purpose string, contributors []Observation, indices []int, out []ContributorFact) error {
	for start := 0; start < len(indices); start += 32 {
		end := start + 32
		if end > len(indices) {
			end = len(indices)
		}
		refs := make([]string, end-start)
		for j := range refs {
			refs[j] = contributors[indices[start+j]].reference
		}
		batch, err := r.Resolve(ctx, key, purpose, refs)
		if err != nil {
			return err
		}
		for j := range batch {
			o := &batch[j]
			origIdx := indices[start+j]
			if !bytes.Equal(o.raw, contributors[origIdx].raw) {
				return ContextUntrusted()
			}
			s := Subject{Caller: o.header.AuthorUID, Kind: o.header.Source.Kind, Source: o.header.Source.ID}
			if o.header.Parent != nil {
				s.Parent = *o.header.Parent
			}
			out[origIdx] = ContributorFact{Subject: s, Eligible: o.eligible}
		}
	}
	return nil
}

// TLSConfiguration returns a detached authority configuration for owned sends.
func (r *NativeReader) TLSConfiguration() *tls.Config {
	return r.self.client.Transport.(*http.Transport).TLSClientConfig.Clone()
}

// Authorities returns the configured identifiers, without learning aliases.
func (r *NativeReader) Authorities() []string {
	ids := make([]string, 0, len(r.pins))
	for id := range r.pins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AttachContributors seals the complete original union before relay access.
func (r *NativeReader) AttachContributors(generation string, originals []Observation) error {
	if len(originals) > 256 {
		return ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.generations[generation]
	if !ok || g.bound {
		return ErrConflict
	}
	seen := map[string]bool{}
	for oi := range originals {
		o := &originals[oi]
		if o.reader != r || o.key != g.key || seen[o.header.Reference] {
			return ErrUnavailable
		}
		seen[o.header.Reference] = true
	}
	g.contributors = append([]Observation(nil), originals...)
	g.bound = true
	r.generations[generation] = g
	return nil
}
