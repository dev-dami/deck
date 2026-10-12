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

// CheckPrivate reports an error when path's DACL grants access to anyone but
// the current user (or has no DACL at all, which grants everyone access).
func CheckPrivate(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
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
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		who := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !who.Equals(sid) {
			return fmt.Errorf("%s is also accessible by %s", path, who.String())
		}
	}
	return nil
}
