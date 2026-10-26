package main

import (
	"regexp"
	"strings"
)

// Region 按节点名称识别出的国家/地区。机场节点命名五花八门（中文、英文、缩写、国旗 emoji），
// 这里用正则尽量覆盖常见写法。与安卓版 Regions.kt 保持一致。
type Region struct {
	Code    string `json:"code"`
	Flag    string `json:"flag"`
	Name    string `json:"name"`
	Pattern string `json:"-"` // Go 正则，同时用于内核策略组的 filter 字段
	re      *regexp.Regexp
}

// GroupName 内核里自动测速组的名字，例如 "🇸🇬 新加坡·自动"（内核侧保持不变）
func (r *Region) GroupName() string { return r.Flag + " " + r.Name + "·自动" }

// PrimaryGroup / BackupGroup 分级时「·自动」下面的两个子组：优先节点组、备用节点组（见 tier.go）
func (r *Region) PrimaryGroup() string { return r.Flag + " " + r.Name + "·优先" }
func (r *Region) BackupGroup() string  { return r.Flag + " " + r.Name + "·备用" }

// DisplayName 界面上显示的地区名：港澳台显示为「中国香港」「中国澳门」「中国台湾」
func (r *Region) DisplayName() string {
	switch r.Code {
	case "HK", "MO", "TW":
		return "中国" + r.Name
	}
	return r.Name
}

// DisplayGroupName 界面上显示的测速组名，例如 "🇭🇰 中国香港·自动"
func (r *Region) DisplayGroupName() string { return r.Flag + " " + r.DisplayName() + "·自动" }

func regexEscape(s string) string { return regexp.QuoteMeta(s) }

// newRegion 构造一个地区：
//   - 短缩写（SG、HK、US…）要求前后都不是英文字母，避免 "US" 误伤 "Australia"
//   - 其他关键词（中文、英文全名、城市名）直接包含即可
func newRegion(code, flag, name string, codes, words []string) *Region {
	all := append([]string{flag, name}, words...)
	var parts []string
	for _, c := range codes {
		parts = append(parts, "(^|[^A-Za-z])"+c+"([^A-Za-z]|$)")
	}
	for _, w := range all {
		parts = append(parts, regexEscape(w))
	}
	p := "(?i)" + strings.Join(parts, "|")
	return &Region{Code: code, Flag: flag, Name: name, Pattern: p, re: regexp.MustCompile(p)}
}

var allRegions = []*Region{
	newRegion("HK", "🇭🇰", "香港", []string{"HK", "HKG"}, []string{"Hong Kong", "HongKong"}),
	newRegion("TW", "🇹🇼", "台湾", []string{"TW", "TWN"}, []string{"臺灣", "Taiwan"}),
	newRegion("SG", "🇸🇬", "新加坡", []string{"SG", "SGP"}, []string{"狮城", "Singapore"}),
	newRegion("JP", "🇯🇵", "日本", []string{"JP", "JPN"}, []string{"东京", "大阪", "Japan", "Tokyo", "Osaka"}),
	newRegion("KR", "🇰🇷", "韩国", []string{"KR", "KOR"}, []string{"韓國", "首尔", "Korea", "Seoul"}),
	newRegion("US", "🇺🇸", "美国", []string{"US", "USA"}, []string{"美國", "United States", "America", "洛杉矶", "硅谷", "西雅图", "纽约", "芝加哥", "Los Angeles", "San Jose", "Seattle", "New York"}),
	newRegion("GB", "🇬🇧", "英国", []string{"UK", "GB", "GBR"}, []string{"英國", "伦敦", "United Kingdom", "Britain", "London"}),
	newRegion("DE", "🇩🇪", "德国", []string{"DE", "DEU"}, []string{"德國", "法兰克福", "Germany", "Frankfurt"}),
	newRegion("FR", "🇫🇷", "法国", []string{"FR", "FRA"}, []string{"法國", "巴黎", "France", "Paris"}),
	newRegion("NL", "🇳🇱", "荷兰", []string{"NL", "NLD"}, []string{"阿姆斯特丹", "Netherlands", "Amsterdam"}),
	newRegion("CA", "🇨🇦", "加拿大", []string{"CA", "CAN"}, []string{"多伦多", "Canada", "Toronto"}),
	newRegion("AU", "🇦🇺", "澳大利亚", []string{"AU", "AUS"}, []string{"澳洲", "悉尼", "Australia", "Sydney"}),
	newRegion("MY", "🇲🇾", "马来西亚", []string{"MY", "MYS"}, []string{"馬來西亞", "Malaysia"}),
	newRegion("TH", "🇹🇭", "泰国", []string{"TH", "THA"}, []string{"曼谷", "Thailand", "Bangkok"}),
	newRegion("VN", "🇻🇳", "越南", []string{"VN", "VNM"}, []string{"Vietnam"}),
	newRegion("PH", "🇵🇭", "菲律宾", []string{"PH", "PHL"}, []string{"Philippines"}),
	newRegion("IN", "🇮🇳", "印度", []string{"IN", "IND"}, []string{"India", "Mumbai"}),
	newRegion("TR", "🇹🇷", "土耳其", []string{"TR", "TUR"}, []string{"Turkey", "Türkiye"}),
	newRegion("AE", "🇦🇪", "阿联酋", []string{"AE", "UAE"}, []string{"迪拜", "Dubai"}),
	newRegion("RU", "🇷🇺", "俄罗斯", []string{"RU", "RUS"}, []string{"莫斯科", "Russia", "Moscow"}),
	newRegion("BR", "🇧🇷", "巴西", []string{"BR", "BRA"}, []string{"Brazil"}),
	newRegion("AR", "🇦🇷", "阿根廷", []string{"AR", "ARG"}, []string{"Argentina"}),
	newRegion("MO", "🇲🇴", "澳门", []string{"MO", "MAC"}, []string{"澳門", "Macau", "Macao"}),
}

// 节点名里出现"剩余/到期/流量/官网/过期"等字样的是信息条目，不是节点
const infoNodePattern = "(?i)剩余|到期|流量|官网|过期|套餐|重置|expire|traffic|群|公告"

var infoNodeRe = regexp.MustCompile(infoNodePattern)

func regionOf(code string) *Region {
	for _, r := range allRegions {
		if r.Code == code {
			return r
		}
	}
	return nil
}

// matchRegion 判断一个节点属于哪个地区。先按国旗/中文名精确匹配，再用正则。
func matchRegion(nodeName string) *Region {
	if infoNodeRe.MatchString(nodeName) {
		return nil
	}
	for _, r := range allRegions {
		if strings.Contains(nodeName, r.Flag) || strings.Contains(nodeName, r.Name) {
			return r
		}
	}
	for _, r := range allRegions {
		if r.re.MatchString(nodeName) {
			return r
		}
	}
	return nil
}

// displayGroup 把内核里的策略组名换成界面显示用的名字（地区自动组用显示名，其余原样返回）
func displayGroup(kernelName string) string {
	for _, r := range allRegions {
		if r.GroupName() == kernelName {
			return r.DisplayGroupName()
		}
	}
	return kernelName
}
