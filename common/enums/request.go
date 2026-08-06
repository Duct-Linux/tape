package enums

type RequestType int8

// RequestType is an enum for the type of request
const (
	RequestTypeEmpty RequestType = iota
	RequestTypePing
	RequestTypeQueryPkg
	RequestTypeDownloadPkg
	RequestTypeRefreshRepos
	RequestTypeLocalInstall
	RequestTypeRemovePkg
	RequestTypeListPkgs
	RequestTypeCheckUpgrades
	RequestTypeSearchPkgs
)
