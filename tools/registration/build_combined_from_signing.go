// tools/registration/build_combined_from_signing.go
// SPDX-License-Identifier: MIT
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

/********** input types **********/

type SigningRow struct {
	Name       string `json:"name"`
	DID        string `json:"did"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	Address    string `json:"address"`
}

type KemAgentRow struct {
	Name          string `json:"name"`
	DID           string `json:"did"`
	Address       string `json:"address"`
	X25519Private string `json:"x25519Private"`
	X25519Public  string `json:"x25519Public"`
}

type kemFile struct {
	Agents []KemAgentRow `json:"agents"`
}

type CombinedRow struct {
	Name          string `json:"name"`
	DID           string `json:"did"`
	PublicKey     string `json:"publicKey"`  // ECDSA (signing JSON)
	PrivateKey    string `json:"privateKey"` // ECDSA (signing JSON)
	Address       string `json:"address"`    // from KEM JSON (preferred)
	X25519Private string `json:"x25519Private"`
	X25519Public  string `json:"x25519Public"`
}

/********** main **********/

func main() {
	signingPath := flag.String("signing", "generated_agent_keys.json", "Signing keys JSON (array)")
	kemPath := flag.String("kem", "keys/kem/generated_kem_keys.json", "KEM keys JSON (object with agents[] or top-level array)")
	out := flag.String("out", "merged_agent_keys.json", "Output merged JSON file")
	agents := flag.String("agents", "", "Comma-separated agent names to include (default: all)")
	flag.Parse()

	// load signing rows
	sRows, err := loadSigning(*signingPath)
	if err != nil {
		fatalf("read signing: %v", err)
	}

	// load KEM rows
	kRows, err := loadKEM(*kemPath)
	if err != nil {
		fatalf("read kem: %v", err)
	}

	// build map[name]KemAgentRow
	kemByName := make(map[string]KemAgentRow, len(kRows))
	for _, kr := range kRows {
		kemByName[strings.TrimSpace(kr.Name)] = kr
	}

	// filter
	filter := parseFilter(*agents)

	var outRows []CombinedRow
	for _, r := range sRows {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		if len(filter) > 0 {
			if _, ok := filter[name]; !ok {
				continue
			}
		}
		kr, ok := kemByName[name]
		if !ok {
			// 강제: KEM 항목 없으면 스킵 (요청 사항)
			fmt.Printf(" - %s: missing KEM entry in %s -> skip\n", name, shortPath(*kemPath))
			continue
		}

		// DID/Address는 KEM JSON 것을 우선 사용
		addr := ensure0x(strings.TrimSpace(kr.Address))
		did := strings.TrimSpace(kr.DID)
		if did == "" {
			// fallback: address로 DID 구성
			if addr == "" {
				did = strings.TrimSpace(r.DID)
			} else {
				did = "did:sage:ethereum:" + addr
			}
		}

		// x25519 필드: 반드시 KEM JSON에서 읽음 (생성하지 않음)
		xpriv := ensure0x(kr.X25519Private)
		xpub := ensure0x(kr.X25519Public)

		// 간단 유효성 체크(선택)
		if err := mustBeHex32(xpub); err != nil {
			fatalf("%s: invalid x25519Public: %v", name, err)
		}
		if err := mustBeHex32(xpriv); err != nil {
			// private은 배포/테스트 용도에서만 필요할 수 있지만, 요청사항에 따라 강제 체크
			fatalf("%s: invalid x25519Private: %v", name, err)
		}

		combined := CombinedRow{
			Name:          name,
			DID:           did,
			PublicKey:     ensure0x(r.PublicKey),
			PrivateKey:    ensure0x(r.PrivateKey),
			Address:       addr,
			X25519Private: xpriv,
			X25519Public:  xpub,
		}
		outRows = append(outRows, combined)
	}

	// ensure output dir exists
	if dir := filepath.Dir(*out); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

	b, err := json.MarshalIndent(outRows, "", "  ")
	if err != nil {
		fatalf("marshal out: %v", err)
	}
	if err := ioutil.WriteFile(*out, b, 0o644); err != nil {
		fatalf("write out: %v", err)
	}
	fmt.Printf("Merged JSON written: %s (rows=%d)\n", *out, len(outRows))
}

/********** helpers **********/

func loadSigning(path string) ([]SigningRow, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []SigningRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func loadKEM(path string) ([]KemAgentRow, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// primary: {"agents":[...]}
	var obj kemFile
	if json.Unmarshal(b, &obj) == nil && len(obj.Agents) > 0 {
		return obj.Agents, nil
	}
	// fallback: top-level array
	var arr []KemAgentRow
	if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
		return arr, nil
	}
	return nil, fmt.Errorf("unrecognized KEM json (need object with agents[] or top-level array)")
}

func parseFilter(csv string) map[string]struct{} {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, p := range strings.Split(csv, ",") {
		q := strings.TrimSpace(p)
		if q != "" {
			out[q] = struct{}{}
		}
	}
	return out
}

func ensure0x(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return s
	}
	return "0x" + s
}

func mustBeHex32(h string) error {
	h = strings.TrimPrefix(strings.TrimSpace(h), "0x")
	// 32바이트(64 hex chars)
	if len(h) != 64 {
		return fmt.Errorf("want 32 bytes (64 hex), got %d", len(h))
	}
	_, err := hex.DecodeString(h)
	return err
}

func shortPath(p string) string { return filepath.Clean(p) }

func fatalf(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+f+"\n", a...)
	os.Exit(1)
}
