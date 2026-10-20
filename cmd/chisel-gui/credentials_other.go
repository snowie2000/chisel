//go:build !windows

package main

import "errors"

func protectProfile(p profile) (profileRecord, error) {
	return profileRecord{profile: p}, nil
}

func restoreProfile(record profileRecord) (profile, error) {
	if record.ProtectedSecrets != "" {
		return profile{}, errors.New("this profile's credentials are protected by Windows; reopen it with the same Windows account")
	}
	return record.profile, nil
}
