package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (rc *registryClient) catalogAll(ctx context.Context) ([]string, error) {
	var all []string
	last := ""
	for i := 0; i < 20; i++ {
		p := "/v2/_catalog?n=500"
		if last != "" {
			p += "&last=" + url.QueryEscape(last)
		}
		if _, body, err := rc.do(ctx, http.MethodGet, p, nil, nil); err != nil {
			return nil, err
		} else {
			var out struct {
				Repositories []string `json:"repositories"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("解析镜像列表失败: %w", err)
			}
			if len(out.Repositories) == 0 {
				break
			}
			all = append(all, out.Repositories...)
			last = out.Repositories[len(out.Repositories)-1]
			if len(out.Repositories) < 500 {
				break
			}
		}
	}
	return all, nil
}

func (rc *registryClient) tagsAll(ctx context.Context, name string) ([]string, error) {
	var all []string
	last := ""
	for i := 0; i < 20; i++ {
		p := "/v2/" + name + "/tags/list?n=500"
		if last != "" {
			p += "&last=" + url.QueryEscape(last)
		}
		if _, body, err := rc.do(ctx, http.MethodGet, p, nil, nil); err != nil {
			return nil, err
		} else {
			var out struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("解析标签列表失败: %w", err)
			}
			if len(out.Tags) == 0 {
				break
			}
			all = append(all, out.Tags...)
			last = out.Tags[len(out.Tags)-1]
			if len(out.Tags) < 500 {
				break
			}
		}
	}
	return all, nil
}

var manifestAccept = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v1+prettyjws",
}

type registryManifestInfo struct {
	Reference string             `json:"reference"`
	Digest    string             `json:"digest"`
	MediaType string             `json:"media_type"`
	Size      int64              `json:"size"`
	Created   string             `json:"created,omitempty"`
	IsList    bool               `json:"is_list"`
	Platforms []registryPlatform `json:"platforms"`
	Config    registryLayer      `json:"config,omitempty"`
	Layers    []registryLayer    `json:"layers"`
}

type registryLayer struct {
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type registryPlatform struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Variant   string `json:"variant,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

func (rc *registryClient) manifest(ctx context.Context, name, reference string) (*registryManifestInfo, error) {
	h := http.Header{}
	h.Set("Accept", strings.Join(manifestAccept, ", "))
	resp, body, err := rc.do(ctx, http.MethodGet, "/v2/"+name+"/manifests/"+reference, h, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("镜像 %s:%s 不存在", name, reference)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("查询失败(%d)", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	mediaType := resp.Header.Get("Content-Type")
	info := &registryManifestInfo{
		Reference: reference, Digest: digest, MediaType: mediaType,
		Size: int64(len(body)),
	}

	var decode struct {
		MediaType string             `json:"mediaType"`
		Manifests []registryPlatform `json:"manifests"`
		Config    struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
			MediaType    string `json:"mediaType"`
			Size         int64  `json:"size"`
			Digest       string `json:"digest"`
		} `json:"config"`
		Layers   []registryLayer   `json:"layers"`
		Platform *registryPlatform `json:"platform"`
	}
	_ = json.Unmarshal(body, &decode)
	if decode.MediaType != "" {
		mediaType = decode.MediaType
	}
	isList := strings.Contains(mediaType, "manifest.list") || strings.Contains(mediaType, "image.index")
	info.IsList = isList
	info.MediaType = mediaType
	info.Layers = decode.Layers
	if decode.Config.Digest != "" {
		info.Config = registryLayer{MediaType: decode.Config.MediaType, Size: decode.Config.Size, Digest: decode.Config.Digest}
	}
	if isList {
		info.Platforms = decode.Manifests
	} else if len(decode.Manifests) == 0 {
		// 单架构镜像：从 config 提取平台，便于前端统一展示
		if decode.Platform != nil {
			info.Platforms = []registryPlatform{*decode.Platform}
		} else if decode.Config.OS != "" || decode.Config.Architecture != "" {
			info.Platforms = []registryPlatform{{OS: decode.Config.OS, Arch: decode.Config.Architecture}}
		}
		// 读取 config blob 获取镜像创建时间（尽力而为，失败不影响详情展示）
		if decode.Config.Digest != "" {
			if cResp, cBody, cErr := rc.do(ctx, http.MethodGet, "/v2/"+name+"/blobs/"+decode.Config.Digest, nil, nil); cErr == nil && cResp.StatusCode == http.StatusOK {
				var cfg struct {
					Created string `json:"created"`
				}
				if json.Unmarshal(cBody, &cfg) == nil && cfg.Created != "" {
					info.Created = cfg.Created
				}
			}
		}
	}
	return info, nil
}

func (rc *registryClient) deleteManifest(ctx context.Context, name, digest, mediaType string) error {
	if digest == "" {
		return fmt.Errorf("缺少 manifest digest，无法删除")
	}
	h := http.Header{}
	h.Set("Accept", strings.Join(manifestAccept, ", "))
	if mediaType != "" {
		h.Set("Content-Type", mediaType)
	}
	resp, body, err := rc.do(ctx, http.MethodDelete, "/v2/"+name+"/manifests/"+digest, h, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("镜像已不存在")
	}
	msg := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("仓库不允许删除操作(%d)——请确认 registry 已开启 delete 功能: %s", resp.StatusCode, msg)
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("删除失败(%d): %s", resp.StatusCode, msg)
}
