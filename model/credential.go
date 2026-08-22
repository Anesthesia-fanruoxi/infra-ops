package model

// Credential SSH 凭据。
type Credential struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Username        string `json:"username"`
	EncryptedSecret []byte `json:"-"`
	Fingerprint     string `json:"fingerprint"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}
