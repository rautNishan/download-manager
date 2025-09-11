package base

type RequestProxyMode string

const RequestProxyModeFollow RequestProxyMode = "follow"
const RequestProxyModeNon RequestProxyMode = "none"
const RequestProxyModeCustom RequestProxyMode = "custom"

type RequestPorxy struct {
	Mode   RequestProxyMode
	Schema string
	Host   string
	Usr    string
	Pwd    string
}
