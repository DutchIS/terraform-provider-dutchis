package dutchis

import (
	"encoding/json"
    "io/ioutil"
    "net/http"
	"log"
	"bytes"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// using a global variable here so that we have an internally accessible
// way to look into our own resource definition. Useful for dynamically doing typecasts
// so that we can print (debug) our ResourceData constructs
var thisResource *schema.Resource

func resourceVirtualServer() *schema.Resource {
	thisResource = &schema.Resource{
		Create:        resourceVmQemuCreate,

		Schema: map[string]*schema.Schema {
			"hostname": {
				Type:     schema.TypeString,
				Required:    true,
				Description: "The virtual server hostname",
			},
			"class": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The Performance class of the virtual server",
			},
			"os": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "OS id of the virtual server",
			},
			"username": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The username of the virtual server. This is ignored on Windows servers",
			},
			"password": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The password of the default user",
			},
			"sshkeys": {
				Type:        schema.TypeList,
				Required:    true,
				Description: "Provide the UUID's of ssh keys or provide a ssh key in openssh format.",
			},
			"cores": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of cores to assign to the virtual server",
			},
			"memory": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of memory in GB to assign to the virtual server",
			},
			"network": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The network speed in Gbps for this virtual server",
			},
			"disk": {
				Type:        schema.TypeInt,
				Required:    true,
				Description: "The amount of storage space in GB to assign to the virtual server",
			},
		},
		Timeouts: resourceTimeouts(),
	}
	return thisResource
}

func resourceVmQemuCreate(d *schema.ResourceData, meta interface{}) error {
/* 	logger, err := CreateSubLogger("virtualserver_create")
	if err != nil {
		return err
	} */

	// DEBUG print out the create request
	/* flatValue, _ := resourceDataToFlatValues(d, thisResource)
	jsonString, _ := json.Marshal(flatValue) */

	providerConfig := meta.(*providerConfiguration)
	lock := dutchisParallelBegin(providerConfig)

	type NewVirtualServer struct {
		hostname string `json:"hostname"`
		class string `json:"class"`
		os string `json:"os"`
		username string `json:"username"`
		password string `json:"password"`
		sshkeys []string `json:"sshkeys"`
		cores int `json:"cores"`
		memory int `json:"memory"`
		network int `json:"network"`
		disk int `json:"disk"`
	}
	
	newVirtualServer := NewVirtualServer{
		hostname: d.Get("hostname").(string),
		class: d.Get("class").(string),
		os: d.Get("os").(string),
		username: d.Get("username").(string),
		password: d.Get("password").(string),
		sshkeys: d.Get("sshkeys").([]string),
		cores: d.Get("cores").(int),
		memory: d.Get("memory").(int),
		network: d.Get("network").(int),
		disk: d.Get("disk").(int),
	}

	body, err := json.Marshal(newVirtualServer)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://dutchis.net/api/v1/virtualservers", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+providerConfig.APIToken)
	req.Header.Add("X-Team-Uuid", providerConfig.TeamUUID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	
    defer resp.Body.Close()
    body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	log.Print("[DEBUG][VirtualServerCreate] virtual server creation done!")
	lock.unlock()
	return nil
}
