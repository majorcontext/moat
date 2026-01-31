package container

import (
	"testing"
)

func TestAppleNetworkManagerImplementsInterface(t *testing.T) {
	var _ NetworkManager = (*appleNetworkManager)(nil)
}
