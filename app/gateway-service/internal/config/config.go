package config

type Config struct {
	Host string `json:",env=HOST,default=0.0.0.0"`
	Port int    `json:",env=PORT,default=7000"`
}
