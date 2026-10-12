//go:build windows

package peer

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// currentUserSID is the SID of the user this process runs as.
func currentUserSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

// restrictPath replaces path's DACL with one protected entry that grants the
// current user full control and nobody else anything. Mode bits mean little on
// Windows, so this is what keeps the identity key private. A directory's entry
// is inherited by what is created in it.
func restrictPath(path string, isDir bool) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	inherit := uint32(windows.NO_INHERITANCE)
	if isDir {
		inherit = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inherit,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}

// ACE types x/sys/windows has no names for. The callback variants carry the
// same header, mask and SID as the plain ones, followed by application data.
const (
	aceDeniedObject         = 0x6
	aceAllowedCallback      = 0x9
	aceDeniedCallback       = 0xA
	aceDeniedCallbackObject = 0xC
)

// CheckPrivate reports an error unless path is owned by the current user and
// its DACL grants access to the current user alone. Deny entries are fine; an
// allow entry for anyone else, or of a kind this check does not read (object
// ACEs), fails it, and so does a missing DACL, which grants everyone access.
// The owner matters as much as the DACL: an owner can always rewrite it.
func CheckPrivate(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trustedOwner(owner, sid) {
		who := "nobody"
		if owner != nil {
			who = owner.String()
		}
		return fmt.Errorf("%s is owned by %s, not by this user", path, who)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%s has no access list, so everyone can read it", path)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE, aceDeniedObject, aceDeniedCallback, aceDeniedCallbackObject:
			continue // only ever takes access away
		case windows.ACCESS_ALLOWED_ACE_TYPE, aceAllowedCallback:
			who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !who.Equals(sid) {
				return fmt.Errorf("%s is also accessible by %s", path, who.String())
			}
		default:
			return fmt.Errorf("%s has an access entry of type %#x that this check cannot verify", path, ace.Header.AceType)
		}
	}
	return nil
}

// trustedOwner accepts the current user. An elevated process creates files
// owned by Administrators (or SYSTEM, for a service), so in one those owners
// are accepted too: they could change any file of this user's anyway.
func trustedOwner(owner, user *windows.SID) bool {
	if owner == nil {
		return false
	}
	if owner.Equals(user) {
		return true
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return false
	}
	return owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) || owner.IsWellKnown(windows.WinLocalSystemSid)
}

// checkTrusted is CheckPrivate: on Windows a file others can read is as bad
// as one they can write, since the DACL is the only guard.
func checkTrusted(path string, _ bool) error { return CheckPrivate(path) }
