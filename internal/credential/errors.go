package credential

import "errors"

// ErrNotFound is returned (wrapped) when no stored credential exists for a
// provider. Match it with errors.Is rather than inspecting the error string.
var ErrNotFound = errors.New("credential not found")

// ErrDecrypt is returned (wrapped) when a stored credential cannot be
// decrypted — usually because the encryption key changed. Match it with
// errors.Is rather than inspecting the error string.
var ErrDecrypt = errors.New("decrypting credential")
