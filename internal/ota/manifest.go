package ota

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ManifestRequest is what GET /api/ota/manifest reads from a phone's request
// headers. Only the first three fields are required; every other header is
// unauthenticated census data, kept only when it is well-formed and small.
type ManifestRequest struct {
	Platform         string // expo-platform
	Runtime          string // expo-runtime-version
	ClientID         string // EAS-Client-ID, lowercase; "" when absent or malformed
	CurrentUpdateID  string // Expo-Current-Update-ID, lowercase; ""
	EmbeddedUpdateID string // Expo-Embedded-Update-ID, lowercase; ""
	Channel          string // am_channel from Expo-Extra-Params: beta, else production
	UserID           int64  // am_uid from Expo-Extra-Params; 0 when absent
	FailedUpdateIDs  []string
	FatalError       string // Expo-Fatal-Error, at most MaxFatalErrorLen characters
}

const (
	maxExtraParamsLen = 1024
	maxFailedIDsLen   = 4096
	maxFailedIDs      = 20
	// MaxFatalErrorLen is how many characters of Expo-Fatal-Error are kept.
	MaxFatalErrorLen = 1024
)

var uidRE = regexp.MustCompile(`^[1-9]\d{0,15}$`)

// ParseManifestRequest validates the request headers of the manifest
// endpoint. An error means 400: expo-protocol-version must be 1, expo-platform
// ios or android, and expo-runtime-version a valid runtime.
func ParseManifestRequest(h http.Header) (*ManifestRequest, error) {
	if v := h.Get("Expo-Protocol-Version"); v != "1" {
		return nil, fmt.Errorf("expo-protocol-version must be 1, not %q", v)
	}
	req := &ManifestRequest{
		Platform: h.Get("Expo-Platform"),
		Runtime:  h.Get("Expo-Runtime-Version"),
		Channel:  ChannelProduction,
	}
	if !ValidPlatform(req.Platform) {
		return nil, fmt.Errorf("expo-platform must be ios or android, not %q", req.Platform)
	}
	if !ValidRuntime(req.Runtime) {
		return nil, errors.New("expo-runtime-version is not a valid runtime version")
	}
	req.ClientID, _ = NormalizeUUID(h.Get("EAS-Client-ID"))
	req.CurrentUpdateID, _ = NormalizeUUID(h.Get("Expo-Current-Update-ID"))
	req.EmbeddedUpdateID, _ = NormalizeUUID(h.Get("Expo-Embedded-Update-ID"))

	if v := h.Get("Expo-Extra-Params"); v != "" && len(v) <= maxExtraParamsLen {
		if members, err := parseSFVDictionary(v); err == nil {
			params, _ := sfvStrings(members)
			if params["am_channel"] == ChannelBeta {
				req.Channel = ChannelBeta
			}
			if uid := params["am_uid"]; uidRE.MatchString(uid) {
				req.UserID, _ = strconv.ParseInt(uid, 10, 64)
			}
		}
	}
	if v := h.Get("Expo-Recent-Failed-Update-IDs"); v != "" && len(v) <= maxFailedIDsLen {
		if items, err := parseSFVList(v); err == nil {
			seen := map[string]bool{}
			for _, it := range items {
				s, _ := it.value.(string)
				id, ok := NormalizeUUID(s)
				if !ok || seen[id] || len(req.FailedUpdateIDs) == maxFailedIDs {
					continue
				}
				seen[id] = true
				req.FailedUpdateIDs = append(req.FailedUpdateIDs, id)
			}
		}
	}
	if v := strings.TrimSpace(h.Get("Expo-Fatal-Error")); v != "" {
		v = strings.ToValidUTF8(v, "�")
		if utf8.RuneCountInString(v) > MaxFatalErrorLen {
			v = string([]rune(v)[:MaxFatalErrorLen])
		}
		req.FatalError = v
	}
	return req, nil
}

// Selection is the manifest endpoint's answer. Parts == nil means 204: no
// update for this phone.
type Selection struct {
	Channel  string // the channel whose pointer applied
	State    string // that pointer's state; "" when there is no pointer
	UpdateID string // the update whose manifest or directive is served
	Parts    []Part
}

// Select picks the answer for a phone (OTAUpdatePlan.md, D2 "Manifest
// endpoint logic"). It returns ErrNotInitialized when the store has no FORMAT
// file. A pointer, runtime or update file that cannot be read is an error, so
// a damaged store is noticed instead of silently freezing phones.
//
// revoked.txt blocks only the update state: rollback-embedded revokes the bad
// update and points at that same update's pre-signed directive, which must
// still be served.
func (s *Store) Select(req *ManifestRequest) (*Selection, error) {
	return s.SelectAt(req, time.Now())
}

// SelectAt is Select at the time now, which decides whether a test_unsigned
// pointer still serves its manifest.
func (s *Store) SelectAt(req *ManifestRequest, now time.Time) (*Selection, error) {
	if err := s.CheckFormat(); err != nil {
		return nil, err
	}
	sel := &Selection{Channel: ChannelProduction}
	var p *Pointer
	if req.Channel == ChannelBeta {
		bp, err := s.ReadPointer(ChannelBeta, req.Platform, req.Runtime)
		if err != nil {
			return nil, err
		}
		if bp != nil {
			sel.Channel, p = ChannelBeta, bp
		}
	}
	if p == nil {
		pp, err := s.ReadPointer(ChannelProduction, req.Platform, req.Runtime)
		if err != nil || pp == nil {
			return sel, err
		}
		p = pp
	}
	sel.State = p.State
	rt, err := s.ReadRuntime(req.Platform, req.Runtime)
	if err != nil || rt == nil {
		return sel, err
	}

	var kind, part string
	switch p.State {
	case StateHalted:
		return sel, nil
	case StateUpdate:
		revoked, err := s.IsRevoked(p.Update)
		if err != nil {
			return nil, err
		}
		if revoked || req.CurrentUpdateID == p.Update {
			return sel, nil
		}
		kind, part = KindManifest, "manifest"
	case StateRollback:
		// Already on the JS built into its binary.
		if req.CurrentUpdateID != "" && req.CurrentUpdateID == req.EmbeddedUpdateID {
			return sel, nil
		}
		kind, part = KindRollback, "directive"
	case StateTestUnsigned:
		return s.selectTestUnsigned(sel, p, req, now)
	}

	info, err := s.ReadUpdateInfo(p.Update)
	if err != nil {
		return nil, err
	}
	if info.Platform != req.Platform || info.Runtime != req.Runtime {
		return nil, fmt.Errorf("update %s is for %s/%s, but the pointer is %s/%s/%s",
			p.Update, info.Platform, info.Runtime, sel.Channel, req.Platform, req.Runtime)
	}
	signed, err := s.ReadSigned(p.Update, kind)
	if err != nil {
		return nil, err
	}
	sel.UpdateID = p.Update
	sel.Parts = []Part{
		{Name: part, ContentType: "application/json; charset=utf-8", Signature: signed.Signature, Body: signed.Body},
		{Name: "certificate_chain", ContentType: "application/x-pem-file", Body: EncodeCertificatePEM(signed.Leaf)},
	}
	return sel, nil
}

// selectTestUnsigned serves the unsigned manifest of `otactl test-unsigned`
// (plan step 6.4) until the pointer's until time: a manifest part with no
// expo-signature and no certificate_chain part. A phone whose binary checks
// signatures must reject it. Pointer.Validate allows this state only on the
// beta channel.
func (s *Store) selectTestUnsigned(sel *Selection, p *Pointer, req *ManifestRequest, now time.Time) (*Selection, error) {
	until, err := time.Parse(time.RFC3339, p.Until)
	if err != nil {
		return nil, err
	}
	if !now.Before(until) || req.CurrentUpdateID == p.Update {
		return sel, nil
	}
	body, info, err := s.ReadTestManifest(p.Update)
	if err != nil {
		return nil, err
	}
	if info.Platform != req.Platform || info.Runtime != req.Runtime {
		return nil, fmt.Errorf("test update %s is for %s/%s, but the pointer is %s/%s/%s",
			p.Update, info.Platform, info.Runtime, sel.Channel, req.Platform, req.Runtime)
	}
	sel.UpdateID = p.Update
	sel.Parts = []Part{{Name: "manifest", ContentType: "application/json; charset=utf-8", Body: body}}
	return sel, nil
}
