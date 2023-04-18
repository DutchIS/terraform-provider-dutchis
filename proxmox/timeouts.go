package dutchis

import (
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceTimeouts() *schema.ResourceTimeout {
	return &schema.ResourceTimeout{
		Create:  schema.DefaultTimeout(20 * time.Minute),
		Read:    schema.DefaultTimeout(20 * time.Minute),
		Update:  schema.DefaultTimeout(20 * time.Minute),
		Delete:  schema.DefaultTimeout(20 * time.Minute),
		Default: schema.DefaultTimeout(20 * time.Minute),
	}
}
