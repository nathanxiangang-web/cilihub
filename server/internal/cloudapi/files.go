package cloudapi

import (
	"path"
	"regexp"
	"strings"
)

const minCloudFileBytes = 100 << 20

var adFileMarker = regexp.MustCompile(`(?i)(^|[^\pL\pN])(ad|ads|advert|advertisement|promo|telegram|t\.me)([^\pL\pN]|$)`)
var adDomainMarker = regexp.MustCompile(`(?i)(^|[^a-z0-9])[a-z0-9][a-z0-9-]{1,62}\.(com|net|org|xyz|top|vip|cc|tv|me)\b`)
var adExemptions = strings.NewReplacer("无广告", "", "免广告", "", "去广告", "")

func isAdvert(name string) bool {
	name = adExemptions.Replace(name)
	for _, marker := range []string{"广告", "发布页", "网址发布", "最新地址", "永久域名", "防止失联", "扫码", "加群", "邀请注册", "推广链接"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return adFileMarker.MatchString(name) || adDomainMarker.MatchString(name)
}

func eligibleCloudFile(name string, size int64) bool {
	return size >= minCloudFileBytes && strings.EqualFold(path.Ext(name), ".mp4") && !isAdvert(name)
}
