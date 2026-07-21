//go:build windows

package securepath

import (
	"os"
	"os/user"

	"golang.org/x/sys/windows"
)

func RestrictToCurrentUser(path string) error {
	current, err := user.Current()
	if err != nil {
		return err
	}
	sid, err := windows.StringToSid(current.Uid)
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if info, statErr := os.Stat(path); statErr != nil {
		return statErr
	} else if info.IsDir() {
		inheritance = windows.CONTAINER_INHERIT_ACE | windows.OBJECT_INHERIT_ACE
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
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
