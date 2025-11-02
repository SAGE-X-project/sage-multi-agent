// tools/registration/build_combined_from_signing.go
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

type SigningRow struct {
	Name       string `json:"name"`
	DID        string `json:"did,omitempty"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

type KemRow struct {
	Name          string `json:"name"`
	DID           string `json:"did,omitempty"`
	Address       string `json:"address,omitempty"`
	X25519PrivHex string `json:"x25519Private,omitempty"`
	X25519PubHex  string `json:"x25519Public,omitempty"`
}

type kemWrapper struct {
	Agents []KemRow `json:"agents"`
}

type CombinedRow struct {
	Name          string `json:"name"`
	DID           string `json:"did"`
	PublicKey     string `json:"publicKey"`
	PrivateKey    string `json:"privateKey"`
	Address       string `json:"address"`
	X25519PrivHex string `json:"x25519Private"`
	X25519PubHex  string `json:"x25519Public"`
}

func readFile(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:], nil
	}
	return b, nil
}

func loadSigningJSON(path string) ([]SigningRow, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	var arr []SigningRow
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("signing json must be a top-level array: %w", err)
	}
	if len(arr) == 0 {
		return nil, errors.New("signing json: empty array")
	}
	for i, r := range arr {
		if r.Name == "" || r.Address == "" || r.PublicKey == "" || r.PrivateKey == "" {
			return nil, fmt.Errorf("signing json: row %d missing required fields (need name,address,publicKey,privateKey)", i)
		}
	}
	return arr, nil
}

func tryLoadKemArray(b []byte) ([]KemRow, error) {
	var arr []KemRow
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

func tryLoadKemWrapper(b []byte) ([]KemRow, error) {
	var w kemWrapper
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	return w.Agents, nil
}

func loadKEMJSON(path string) ([]KemRow, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if rows, err := tryLoadKemArray(b); err == nil {
		return rows, nil
	}
	if rows, err := tryLoadKemWrapper(b); err == nil {
		return rows, nil
	}
	return nil, fmt.Errorf("kem json not recognized: %s (need {\"agents\":[]} or top-level array)", path)
}

func toSet(csv string) map[string]struct{} {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	S := map[string]struct{}{}
	for _, t := range strings.Split(csv, ",") {
		n := strings.TrimSpace(t)
		if n != "" {
			S[n] = struct{}{}
		}
	}
	return S
}

func filterSigning(in []SigningRow, allow map[string]struct{}) []SigningRow {
	if allow == nil {
		return in
	}
	out := make([]SigningRow, 0, len(in))
	for _, r := range in {
		if _, ok := allow[r.Name]; ok {
			out = append(out, r)
		}
	}
	return out
}

func indexKem(rows []KemRow) map[string]KemRow {
	m := make(map[string]KemRow, len(rows))
	for _, r := range rows {
		m[r.Name] = r
	}
	return m
}

func chooseDID(sign SigningRow, kem KemRow) string {
	if sign.DID != "" {
		return sign.DID
	}
	if kem.DID != "" {
		return kem.DID
	}
	return "did:sage:ethereum:" + sign.Address
}

func merge(signing []SigningRow, kemIdx map[string]KemRow) []CombinedRow {
	out := make([]CombinedRow, 0, len(signing))
	for _, s := range signing {
		k := kemIdx[s.Name]
		cr := CombinedRow{
			Name:          s.Name,
			DID:           chooseDID(s, k),
			PublicKey:     s.PublicKey,
			PrivateKey:    s.PrivateKey,
			Address:       s.Address,
			X25519PrivHex: k.X25519PrivHex, // may be ""
			X25519PubHex:  k.X25519PubHex,  // may be ""
		}
		out = append(out, cr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func main() {
	var (
		signingPath string
		kemPath     string
		outPath     string
		agentsCSV   string
	)
	flag.StringVar(&signingPath, "signing", "", "path to signing keys JSON (top-level array)")
	flag.StringVar(&kemPath, "kem", "", "optional path to KEM keys JSON (top-level array or {\"agents\":[]})")
	flag.StringVar(&outPath, "out", "", "path to write merged JSON")
	flag.StringVar(&agentsCSV, "agents", "", "optional filter: comma-separated agent names")
	flag.Parse()

	if signingPath == "" || outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: -signing <file.json> [-kem <file.json>] -out <merged.json> [-agents \"a,b,c\"]")
		os.Exit(2)
	}

	signRows, err := loadSigningJSON(signingPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load signing:", err)
		os.Exit(1)
	}

	filter := toSet(agentsCSV)
	signRows = filterSigning(signRows, filter)

	kemRows, err := loadKEMJSON(kemPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	merged := merge(signRows, indexKem(kemRows))

	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal merged:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write merged:", err)
		os.Exit(1)
	}
	fmt.Printf("Merged %d agents -> %s\n", len(merged), outPath)
}
