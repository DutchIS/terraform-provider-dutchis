package dutchis

import (
	"encoding/json"
    "io/ioutil"
    "net/http"
	"bytes"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// using a global variable here so that we have an internally accessible
// way to look into our own resource definition. Useful for dynamically doing typecasts
// so that we can print (debug) our ResourceData constructs
var thisResource *schema.Resource

func resourceVirtualServer() *schema.Resource {
	thisResource = &schema.Resource{
		Create: resourceVirtualServerCreate,
		Read: resourceVirtualServerRead,
		Delete: resourceVirtualServerDelete,
		Update: resourceVirtualServerUpdate,

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
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"cores": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    false,
				Description: "The amount of cores to assign to the virtual server",
			},
			"memory": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    false,
				Description: "The amount of memory in GB to assign to the virtual server",
			},
			"network": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    false,
				Description: "The network speed in Gbps for this virtual server",
			},
			"disk": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    false,
				Description: "The amount of storage space in GB to assign to the virtual server",
			},
		},
		Timeouts: resourceTimeouts(),
	}
	return thisResource
}

func resourceVirtualServerCreate(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*providerConfiguration)
	lock := parallelBegin(providerConfig)

	logger, err := CreateSubLogger("resourceVirtualServerCreate")
	if err != nil {
		return err
	}

	var sshKeys []string
	for _, sshKey := range d.Get("sshkeys").([]interface{}) {
		sshKeys = append(sshKeys, sshKey.(string))
	}

	logger.Info().Msg("Parsed ssh keys from config")

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
		Sshkeys: sshKeys,
		Cores: d.Get("cores").(int),
		Memory: d.Get("memory").(int),
		Network: d.Get("network").(int),
		Disk: d.Get("disk").(int),
	}

	logger.Info().Msg("Creating new virtual server")

	body, err := json.Marshal(newVirtualServer)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to marshal JSON")
		return err
	}

	req, err := http.NewRequest("POST", "https://dutchis.net/api/v1/virtualservers", bytes.NewBuffer(body))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create HTTP request")
		return err
	}

	req.Header.Add("Authorization", "Bearer "+providerConfig.APIToken)
	req.Header.Add("X-Team-Uuid", providerConfig.TeamUUID)
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to send HTTP request")
		return err
	}

    defer resp.Body.Close()
    body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to read HTTP response")
		return err
	}

	type NewVirtualServerResponse struct {
		Success bool `json:"success"`
		Message string `json:"message"`
		UUID string `json:"uuid"`
	}

    var virtualserver NewVirtualServerResponse
    if err := json.Unmarshal(body, &virtualserver); err != nil { 
		logger.Error().Err(err).Msg("Failed to unmarshal JSON")
        return err
    }

    d.SetId(virtualserver.UUID)

	logger.Info().Msg("Created new virtual server")
	lock.unlock()
	return resourceVirtualServerRead(d, meta)
}

func resourceVirtualServerRead(d *schema.ResourceData, meta interface{}) error {
	pconf := meta.(*providerConfiguration)
	lock := parallelBegin(pconf)
	defer lock.unlock()
	providerConfig := meta.(*providerConfiguration)
	
	logger, err := CreateSubLogger("resourceVirtualServerRead")
	if err != nil {
		return err
	}

	logger.Info().Msg("Reading virtual server: " + d.Id())
	req, err := http.NewRequest("GET", "https://dutchis.net/api/v1/virtualservers/" + d.Id(), nil)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create HTTP request")
		return err
	}
	req.Header.Add("Authorization", "Bearer "+providerConfig.APIToken)
	req.Header.Add("X-Team-Uuid", providerConfig.TeamUUID)
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	
    defer resp.Body.Close()
    body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to read HTTP response")
		return err
	}
	
	type VirtualServer struct {
		Success bool `json:"success"`
		Data struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
			Class string `json:"class"`
			Status string `json:"status"`
			Node string `json:"node"`
			Cpus int `json:"cpus"`
			Maxmem int `json:"maxmem"`
			Maxdisk int `json:"maxdisk"`
			Installing bool `json:"installing"`
		} `json:"data"`
	}

    var virtualserver VirtualServer
    if err := json.Unmarshal(body, &virtualserver); err != nil { 
		logger.Error().Err(err).Msg("Failed to unmarshal JSON")
        return err
    }

	d.Set("hostname", virtualserver.Data.Name)
	d.Set("class", virtualserver.Data.Class)
	d.Set("cores", virtualserver.Data.Cpus)
	d.Set("memory", virtualserver.Data.Maxmem)
	d.Set("disk", virtualserver.Data.Maxdisk)

	logger.Info().Msg("Read configuration for virtual server: " + d.Id())

	return nil
}

func resourceVirtualServerDelete(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*providerConfiguration)
	lock := parallelBegin(providerConfig)
	defer lock.unlock()
	
	logger, err := CreateSubLogger("resourceVirtualServerDelete")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", "https://dutchis.net/api/v1/virtualservers/" + d.Id(), nil)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create HTTP request")
		return err
	}
	req.Header.Add("Authorization", "Bearer "+providerConfig.APIToken)
	req.Header.Add("X-Team-Uuid", providerConfig.TeamUUID)
	_, err = http.DefaultClient.Do(req)

	return err
}

func resourceVirtualServerUpdate(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*providerConfiguration)
	lock := parallelBegin(providerConfig)

	logger, err := CreateSubLogger("resourceVirtualServerUpdate")
	if err != nil {
		return err
	}

	type UpdateVirtualServer struct {
		Cores int `json:"cores"`
		Memory int `json:"memory"`
		Network int `json:"network"`
		Disk int `json:"disk"`
	}
	
	updateVirtualServer := UpdateVirtualServer{
		Cores: d.Get("cores").(int),
		Memory: d.Get("memory").(int),
		Network: d.Get("network").(int),
		Disk: d.Get("disk").(int),
	}
	
	logger.Info().Msg("Deleting virtual server")

	body, err := json.Marshal(updateVirtualServer)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to marshal JSON")
		return err
	}

	req, err := http.NewRequest("PATCH", "https://dutchis.net/api/v1/virtualservers/" + d.Id() + "/specs", bytes.NewBuffer(body))
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create HTTP request")
		return err
	}
	req.Header.Add("Authorization", "Bearer "+providerConfig.APIToken)
	req.Header.Add("X-Team-Uuid", providerConfig.TeamUUID)
	_, err = http.DefaultClient.Do(req)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to send HTTP request")
		return err
	}

    d.SetId("")

	logger.Info().Msg("Deleted virtual server")
	lock.unlock()
	return resourceVirtualServerRead(d, meta)
}
