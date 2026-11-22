// Package geo 提供 ISO 3166-1 alpha-2 国家/地区代码的规范化、中文名和国旗 emoji。
package geo

import "strings"

// aliases 把常见的非标准写法映射到 ISO 代码。
var aliases = map[string]string{
	"UK": "GB",
	"EL": "GR",
}

// names 是常见国家/地区代码到中文名的映射；未收录的代码以代码本身作为名称。
var names = map[string]string{
	"AE": "阿联酋", "AR": "阿根廷", "AT": "奥地利", "AU": "澳大利亚", "BE": "比利时",
	"BG": "保加利亚", "BR": "巴西", "CA": "加拿大", "CH": "瑞士", "CL": "智利",
	"CN": "中国", "CO": "哥伦比亚", "CY": "塞浦路斯", "CZ": "捷克", "DE": "德国",
	"DK": "丹麦", "EE": "爱沙尼亚", "EG": "埃及", "ES": "西班牙", "FI": "芬兰",
	"FR": "法国", "GB": "英国", "GR": "希腊", "HK": "香港", "HR": "克罗地亚",
	"HU": "匈牙利", "ID": "印度尼西亚", "IE": "爱尔兰", "IL": "以色列", "IN": "印度",
	"IS": "冰岛", "IT": "意大利", "JP": "日本", "KH": "柬埔寨", "KR": "韩国",
	"KZ": "哈萨克斯坦", "LT": "立陶宛", "LU": "卢森堡", "LV": "拉脱维亚", "MO": "澳门",
	"MN": "蒙古", "MX": "墨西哥", "MY": "马来西亚", "NG": "尼日利亚", "NL": "荷兰",
	"NO": "挪威", "NZ": "新西兰", "PE": "秘鲁", "PH": "菲律宾", "PK": "巴基斯坦",
	"PL": "波兰", "PT": "葡萄牙", "RO": "罗马尼亚", "RS": "塞尔维亚", "RU": "俄罗斯",
	"SA": "沙特阿拉伯", "SE": "瑞典", "SG": "新加坡", "SI": "斯洛文尼亚", "SK": "斯洛伐克",
	"TH": "泰国", "TR": "土耳其", "TW": "台湾", "UA": "乌克兰", "US": "美国",
	"VN": "越南", "ZA": "南非",
}

// Normalize 把代码转为大写并解析别名；不是两位字母时返回空串。
func Normalize(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	if a, ok := aliases[c]; ok {
		c = a
	}
	if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return ""
	}
	return c
}

// Name 返回中文名；未收录时返回代码本身。
func Name(code string) string {
	if n, ok := names[code]; ok {
		return n
	}
	return code
}

// Flag 把两位代码换算成由两个 regional indicator 符号组成的国旗 emoji。
func Flag(code string) string {
	c := Normalize(code)
	if c == "" {
		return ""
	}
	const base = 0x1F1E6
	return string([]rune{rune(base + int(c[0]-'A')), rune(base + int(c[1]-'A'))})
}
