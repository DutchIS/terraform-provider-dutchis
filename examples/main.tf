terraform {
    required_providers {
        dutchis = {
            source  = "dutchis/terraform"
            version = "1.1.8"
        }
    }
}

provider "dutchis" {
    dutchis_team_uuid = "uuid"
    dutchis_api_token = "token"
}

resource "virtualserver" "example-vs" {
    count = 3 # Amount to create
    hostname = "server-${count.index}" # Hostname of the virtual server
    class = "performance" # Performance class
    os = "ubuntu2204" # OS
    username = "exampleuser" # Ignored on windows systems
    password = "ferrysekur" # Default user's password
    sshkeys = [
        "0b4d8fc1-ddea-11ed-b7cc-9a049341f472"
    ]
    cores = 2 # Number of cores
    memory = 4 # Memory in GB
    network = 1 # Network speed in Gbps
    disk = 50 # Disk speed in GB
}
