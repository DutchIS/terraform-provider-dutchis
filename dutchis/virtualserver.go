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
		Create: resourceVmQemuCreate,

		Schema: map[string]*schema.Schema {
			"hostname": {
				Type:     schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The virtual server hostname",
			},
			"class": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The Performance class of the virtual server",
			},
			"os": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "OS id of the virtual server",
			},
			"username": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The username of the virtual server. This is ignored on Windows servers",
			},
			"password": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The password of the default user",
			},
			"sshkeys": {
				Type:        schema.TypeList,
				Required:    true,
				ForceNew:    true,
				Description: "Provide the UUID's of ssh keys or provide a ssh key in openssh format.",
			},
			"cores": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The amount of cores to assign to the virtual server",
			},
			"memory": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The amount of memory in GB to assign to the virtual server",
			},
			"network": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The network speed in Gbps for this virtual server",
			},
			"disk": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The amount of storage space in GB to assign to the virtual server",
			},
		},
		Timeouts: resourceTimeouts(),
	}
	return thisResource
}

func resourceVmQemuCreate(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*providerConfiguration)
	lock := dutchisParallelBegin(providerConfig)

	type NewVirtualServer struct {
		Hostname string `json:"hostname"`
		Class string `json:"class"`
		Os string `json:"os"`
		Username string `json:"username"`
		Password string `json:"password"`
		Sshkeys []string `json:"sshkeys"`
		Cores int `json:"cores"`
		Memory int `json:"memory"`
		Network int `json:"network"`
		Disk int `json:"disk"`
	}
	
	newVirtualServer := NewVirtualServer{
		Hostname: d.Get("hostname").(string),
		Class: d.Get("class").(string),
		Os: d.Get("os").(string),
		Username: d.Get("username").(string),
		Password: d.Get("password").(string),
		Sshkeys: d.Get("sshkeys").([]string),
		Cores: d.Get("cores").(int),
		Memory: d.Get("memory").(int),
		Network: d.Get("network").(int),
		Disk: d.Get("disk").(int),
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
