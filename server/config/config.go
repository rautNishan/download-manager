package config

func (conf *Config) Init() {
	if conf.Host == "" {
		conf.Host = "localhost"
	}
	if conf.Port == 0 {
		conf.Port = 3000
	}
	if conf.RefreshInterval == 0 {
		conf.RefreshInterval = 300 //This is in miliseconds
	}
	if conf.StorageDir == "" {
		conf.StorageDir = "./server/storage"
	}
	if conf.Storage == "" {
		conf.Storage = "bolt"
	}
}
