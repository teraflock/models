package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Remote verification: does the artifact host still serve the bytes the
// manifest pins?
//
// A manifest's sha256/size_bytes are copied from the Hugging Face tree API
// when the entry is written, and nothing in the repository notices when
// the publisher later re-uploads the file under the same name. On
// 2026-11-01 seven of 67 artifacts had drifted that way; every node that
// downloaded one failed sha256 verification after 18-28 GB. VerifyRemote
// re-reads the tree API for every folder the catalog references and
// reports each artifact whose lfs.oid or lfs.size no longer matches, so
// the drift fails a scheduled run and the promote workflow instead of a
// download on a node. It is the only check in this tool that needs the
// network, which is why it is opt-in (-verify-remote) and never part of
// the PR path.

// RemoteOptions configures VerifyRemote. The zero value talks to
// huggingface.co with a 60s-per-request client and the HF_TOKEN
// environment variable, if set, as a bearer token (public and gated repos
// expose their tree anonymously today; the token is for rate limits and
// for a repo that ever stops doing so).
type RemoteOptions struct {
	// BaseURL of the Hugging Face hub API (default https://huggingface.co);
	// tests point it at an httptest server.
	BaseURL string
	Client  *http.Client
	Token   string
	// Logf receives one line per folder fetched; nil discards.
	Logf func(format string, args ...any)
}

// RemoteReport is the outcome of VerifyRemote. Issues is empty on success;
// Checked counts the artifacts whose pinned hash and size matched,
// Skipped the ones that could not be compared and why (a TODO-verify
// placeholder, a host that is not huggingface.co). A skipped artifact is
// not a failure: the release gate already refuses placeholders, and a
// non-HF URL has no tree API to ask.
type RemoteReport struct {
	Issues  []string
	Checked int
	Skipped []string
	Folders int
}

// remoteArtifact is one pinned file: where it lives upstream and what the
// manifest says about it.
type remoteArtifact struct {
	Label  string // "catalog/x.yaml quant Q8_0" (plus " parts[i]" / " mmproj")
	URL    string
	SHA256 string
	Size   int64
}

// hfLocation is a huggingface.co resolve URL taken apart.
type hfLocation struct {
	Repo   string // owner/name
	Rev    string // main, a tag or a commit
	Folder string // "" for the repo root
	File   string // basename
}

var hfResolveRE = regexp.MustCompile(`^/([^/]+/[^/]+)/resolve/([^/]+)/(.+)$`)

// parseHFResolve splits https://huggingface.co/<owner>/<repo>/resolve/<rev>/<path>
// into its parts; ok is false for any other URL.
func parseHFResolve(raw string) (loc hfLocation, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "huggingface.co") {
		return loc, false
	}
	m := hfResolveRE.FindStringSubmatch(u.Path)
	if m == nil {
		return loc, false
	}
	loc.Repo, loc.Rev = m[1], m[2]
	loc.Folder, loc.File = path.Split(m[3])
	loc.Folder = strings.TrimSuffix(loc.Folder, "/")
	return loc, loc.File != ""
}

// treeKey identifies one tree API call.
func (l hfLocation) treeKey() string { return l.Repo + "@" + l.Rev + "/" + l.Folder }

// hfTreeEntry is the subset of a tree API row this check reads.
type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

// collectRemoteArtifacts lists every pinned file in catalog/*.yaml:
// single-file quants, every shard of a sharded quant, and mmproj sidecars.
func collectRemoteArtifacts(root string) ([]remoteArtifact, error) {
	files, err := filepath.Glob(filepath.Join(root, "catalog", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []remoteArtifact
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		rel := filepath.ToSlash(filepath.Join("catalog", filepath.Base(f)))
		for _, q := range m.Quants {
			label := fmt.Sprintf("%s quant %s", rel, q.Quant)
			if len(q.Parts) == 0 {
				out = append(out, remoteArtifact{Label: label, URL: q.ArtifactURL, SHA256: q.SHA256, Size: q.SizeBytes})
			}
			for i, p := range q.Parts {
				out = append(out, remoteArtifact{Label: fmt.Sprintf("%s parts[%d]", label, i), URL: p.URL, SHA256: p.SHA256, Size: p.SizeBytes})
			}
			if q.Mmproj != nil {
				out = append(out, remoteArtifact{Label: label + " mmproj", URL: q.Mmproj.URL, SHA256: q.Mmproj.SHA256, Size: q.Mmproj.SizeBytes})
			}
		}
	}
	return out, nil
}

// VerifyRemote compares every pinned artifact under root/catalog with what
// the Hugging Face tree API reports for it today. The returned error is
// for the check itself failing (a folder could not be fetched, the API
// answered something unparseable): the caller must treat that as "not
// verified", never as a pass. Mismatches come back in Report.Issues.
func VerifyRemote(root string, opts RemoteOptions) (RemoteReport, error) {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://huggingface.co"
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Token == "" {
		opts.Token = os.Getenv("HF_TOKEN")
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	arts, err := collectRemoteArtifacts(root)
	if err != nil {
		return RemoteReport{}, err
	}

	var rep RemoteReport
	// Group by folder so each tree listing is fetched once.
	type want struct {
		art remoteArtifact
		loc hfLocation
	}
	groups := map[string][]want{}
	for _, a := range arts {
		if a.SHA256 == shaPlaceholder {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: sha256 is %s — nothing pinned to compare", a.Label, shaPlaceholder))
			continue
		}
		loc, ok := parseHFResolve(a.URL)
		if !ok {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %s is not a huggingface.co resolve URL — no tree API to check against", a.Label, a.URL))
			continue
		}
		groups[loc.treeKey()] = append(groups[loc.treeKey()], want{a, loc})
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ws := groups[k]
		loc := ws[0].loc
		entries, err := fetchHFTree(opts, loc)
		if err != nil {
			return rep, fmt.Errorf("%s (rev %s, folder %q): %w", loc.Repo, loc.Rev, loc.Folder, err)
		}
		rep.Folders++
		logf("%s@%s/%s: %d entries", loc.Repo, loc.Rev, loc.Folder, len(entries))
		byName := map[string]hfTreeEntry{}
		for _, e := range entries {
			byName[path.Base(e.Path)] = e
		}
		for _, w := range ws {
			e, found := byName[w.loc.File]
			switch {
			case !found || e.Type != "file":
				rep.Issues = append(rep.Issues, fmt.Sprintf("%s: %s no longer exists upstream (folder %s@%s/%s)", w.art.Label, w.loc.File, loc.Repo, loc.Rev, loc.Folder))
			case e.LFS == nil:
				rep.Issues = append(rep.Issues, fmt.Sprintf("%s: %s is not an LFS object upstream — no sha256 to compare (size %d)", w.art.Label, w.loc.File, e.Size))
			default:
				bad := false
				if e.LFS.OID != w.art.SHA256 {
					bad = true
					rep.Issues = append(rep.Issues, fmt.Sprintf("%s: sha256 drift — manifest %s, upstream %s (%s re-uploaded; re-pin from the tree API, never by hand)", w.art.Label, w.art.SHA256, e.LFS.OID, w.loc.File))
				}
				if e.LFS.Size != w.art.Size {
					bad = true
					rep.Issues = append(rep.Issues, fmt.Sprintf("%s: size_bytes drift — manifest %d, upstream %d (%s)", w.art.Label, w.art.Size, e.LFS.Size, w.loc.File))
				}
				if !bad {
					rep.Checked++
				}
			}
		}
	}
	return rep, nil
}

// fetchHFTree lists one folder of one repo revision, following the API's
// Link: <...>; rel="next" pagination.
func fetchHFTree(opts RemoteOptions, loc hfLocation) ([]hfTreeEntry, error) {
	base, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	p := path.Join("/api/models", loc.Repo, "tree", loc.Rev)
	if loc.Folder != "" {
		p = path.Join(p, loc.Folder)
	}
	next := base.ResolveReference(&url.URL{Path: p}).String()

	var all []hfTreeEntry
	for page := 0; next != ""; page++ {
		if page > 50 {
			return nil, fmt.Errorf("more than 50 pages of tree listing — refusing to continue")
		}
		req, err := http.NewRequest(http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "teraflock-models-validate/verify-remote")
		if opts.Token != "" {
			req.Header.Set("Authorization", "Bearer "+opts.Token)
		}
		resp, err := opts.Client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: HTTP %d: %s", next, resp.StatusCode, strings.TrimSpace(string(firstN(body, 200))))
		}
		var page []hfTreeEntry
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("GET %s: decode: %w", next, err)
		}
		all = append(all, page...)
		next = nextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

var nextLinkRE = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func nextLink(h string) string {
	m := nextLinkRE.FindStringSubmatch(h)
	if m == nil {
		return ""
	}
	return m[1]
}

func firstN(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
