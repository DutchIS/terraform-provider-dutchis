package dutchis

import (
	"fmt"
    "net/http"
    "encoding/json"
    "io/ioutil"
	"strconv"
	"sync"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type providerConfiguration struct {
	MaxParallel                        int
	CurrentParallel                    int
	Mutex                              *sync.Mutex
	Cond                               *sync.Cond
	LogFile                            string
	LogLevels                          map[string]string
	TeamUUID 						   string
	APIToken 						   string
}

// Provider - Terrafrom properties for dutchis
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"dutchis_team_uuid": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("DUTCHIS_TEAM_UUID", nil),
				Description: "Team UUID to which to deploy to.",
			},
			"dutchis_api_token": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("DUTCHIS_API_TOKEN", nil),
				Description: "API Secret",
				Sensitive:   true,
			},
			"dutchis_log_enable": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Enable provider logging to get DutchIS API logs",
			},
			"dutchis_log_levels": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Configure the logging level to display; trace, debug, info, warn, etc",
			},
			"dutchis_log_file": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "terraform-plugin-dutchis.log",
				Description: "Write logs to this specific file",
			},
		},

		ResourcesMap: map[string]*schema.Resource{
			"dutchis_virtualserver":  resourceVirtualServer(),
		},

		ConfigureFunc: providerConfigure,
	}
}

func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	// Minimum permissions check
	minimumPermissions := []string{
		"virtualserver:read",
		"virtualserver:create",
		"virtualserver:update",
		"virtualserver:power",
		"virtualserver:delete",
		"virtualserver:upgrade",
	}

	req, err := http.NewRequest("GET", "https://dutchis.net/api/v1/permissions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+d.Get("dutchis_api_token").(string))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	
    defer resp.Body.Close()
    body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	type Permissions struct {
		success bool `json:"success"`
		permissions []string `json:"permissions"`
	}
    var result Permissions
    if err := json.Unmarshal(body, &result); err != nil { 
        return nil, err
    }

	for _, permission := range minimumPermissions {
		if !Contains(result.permissions, permission) {
			return nil, fmt.Errorf("missing permission %v", permission)
		}
    }

	logLevels := make(map[string]string)
	for logger, level := range d.Get("pm_log_levels").(map[string]interface{}) {
		levelAsString, ok := level.(string)
		if ok {
			logLevels[logger] = levelAsString
		} else {
			return nil, fmt.Errorf("invalid logging level %v for %v. Be sure to use a string", level, logger)
		}
	}

	// actually configure logging
	// note that if enable is false here, the configuration will squash all output
	ConfigureLogger(
		d.Get("dutchis_log_enable").(bool),
		d.Get("dutchis_log_file").(string),
		logLevels,
	)

	var mut sync.Mutex
	return &providerConfiguration{
		MaxParallel:                        d.Get("pm_parallel").(int),
		CurrentParallel:                    0,
		Mutex:                              &mut,
		Cond:                               sync.NewCond(&mut),
		LogFile:                            d.Get("dutchis_log_file").(string),
		LogLevels:                          logLevels,
		TeamUUID: 						    d.Get("dutchis_team_uuid").(string),
		APIToken: 						    d.Get("dutchis_api_token").(string),
	}, nil
}

type apiLockHolder struct {
	locked bool
	conf  *providerConfiguration
}

func (lock *apiLockHolder) lock() {
	if lock.locked {
		return
	}
	lock.locked = true
	conf := lock.conf
	conf.Mutex.Lock()
	for conf.CurrentParallel >= conf.MaxParallel {
		conf.Cond.Wait()
	}
	conf.CurrentParallel++
	conf.Mutex.Unlock()
}

func (lock *apiLockHolder) unlock() {
	if !lock.locked {
		return
	}
	lock.locked = false
	conf := lock.conf
	conf.Mutex.Lock()
	conf.CurrentParallel--
	conf.Cond.Signal()
	conf.Mutex.Unlock()
}

func dutchisParallelBegin(conf *providerConfiguration) *apiLockHolder {
	lock := &apiLockHolder{
		conf:  conf,
		locked: false,
	}
	lock.lock()
	return lock
}

func resourceId(targetNode string, resType string, vmId int) string {
	return fmt.Sprintf("%s/%s/%d", targetNode, resType, vmId)
}

func parseResourceId(resId string) (targetNode string, resType string, vmId int, err error) {
	if !rxRsId.MatchString(resId) {
		return "", "", -1, fmt.Errorf("invalid resource format: %s. Must be <node>/<type>/<vmid>", resId)
	}
	idMatch := rxRsId.FindStringSubmatch(resId)
	targetNode = idMatch[1]
	resType = idMatch[2]
	vmId, err = strconv.Atoi(idMatch[3])
	return
}

func clusterResourceId(resType string, resId string) string {
	return fmt.Sprintf("%s/%s", resType, resId)
}

func parseClusterResourceId(resId string) (resType string, id string, err error) {
	if !rxClusterRsId.MatchString(resId) {
		return "", "", fmt.Errorf("invalid resource format: %s. Must be <type>/<resourceid>", resId)
	}
	idMatch := rxClusterRsId.FindStringSubmatch(resId)
	return idMatch[1], idMatch[2], nil
}
