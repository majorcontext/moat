package secrets_test

import (
	"fmt"

	"github.com/majorcontext/moat/internal/secrets"
)

func ExampleParseScheme() {
	fmt.Println(secrets.ParseScheme("op://vault/item/field"))
	fmt.Println(secrets.ParseScheme("aws-sm://my-secret"))
	fmt.Printf("%q\n", secrets.ParseScheme("plain-text-no-scheme")) // no scheme
	// Output:
	// op
	// aws-sm
	// ""
}
