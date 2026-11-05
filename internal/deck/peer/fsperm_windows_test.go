//go:build windows

package peer

import (
	"testing"

	"golang.org/x/sys/windows"
)

// shareWithOthers gives path a protected DACL that grants the current user
// and Everyone full control.
func shareWithOthers(t *testing.T, path string) {
	t.Helper()
	setDACL(t, path, windows.GRANT_ACCESS)
}

func setDACL(t *testing.T, path string, everyone windows.ACCESS_MODE) {
	t.Helper()
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	entry := func(sid *windows.SID, mode windows.ACCESS_MODE, typ windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        mode,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  typ,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		entry(user, windows.GRANT_ACCESS, windows.TRUSTEE_IS_USER),
		entry(world, everyone, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

// currentUserSID is the SID of the user this process runs as.
func currentUserSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}
