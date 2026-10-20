package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"unsafe"

	"golang.org/x/sys/windows"
)

type credentials struct {
	Auth    string `json:"auth"`
	Proxy   string `json:"proxy"`
	Headers string `json:"headers"`
}

func protectProfile(p profile) (profileRecord, error) {
	record := profileRecord{profile: p}
	data, err := json.Marshal(credentials{p.Auth, p.Proxy, p.Headers})
	if err != nil {
		return record, err
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return record, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	record.ProtectedSecrets = base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, int(out.Size)))
	record.Auth = ""
	record.Headers = ""
	if u, err := url.Parse(record.Proxy); err == nil {
		u.User = nil
		record.Proxy = u.String()
	}
	return record, nil
}

func restoreProfile(record profileRecord) (profile, error) {
	p := record.profile
	if record.ProtectedSecrets == "" {
		return p, nil
	} // Existing plaintext profiles migrate on the next save.
	data, err := base64.StdEncoding.DecodeString(record.ProtectedSecrets)
	if err != nil {
		return p, err
	}
	if len(data) == 0 {
		return p, nil
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return p, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	var secrets credentials
	if err := json.Unmarshal(unsafe.Slice(out.Data, int(out.Size)), &secrets); err != nil {
		return p, err
	}
	p.Auth, p.Proxy, p.Headers = secrets.Auth, secrets.Proxy, secrets.Headers
	return p, nil
}
