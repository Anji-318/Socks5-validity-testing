package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"flag"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oschwald/geoip2-golang"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/net/proxy"
	"gopkg.in/ini.v1"
)

// Config 结构体用于映射 config.ini 文件的内容
type Config struct {
	Telegram struct {
		BotToken string `ini:"bot_token"`
		ChatID   string `ini:"chat_id"`
	} `ini:"telegram"`
	Settings struct {
		PresetProxy   []string `ini:"preset_proxy"`
		FdipDir       string   `ini:"fdip_dir"`
		OutputDir     string   `ini:"output_dir"`
		CheckTimeout  int      `ini:"check_timeout"`
		MaxConcurrent int      `ini:"max_concurrent"`
		SpeedTestURL  string   `ini:"speed_test_url"`
	} `ini:"settings"`
}

var (
	config    Config
	logFile   *os.File
	logMutex  sync.Mutex
)

// LogWriter 是一个实现了 io.Writer 接口的结构体，用于将日志同时写入文件和控制台，并移除时间戳
type LogWriter struct{}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	logMutex.Lock()
	defer logMutex.Unlock()

	// 屏蔽 Telegram Bot Token
	logStr := string(p)
	if config.Telegram.BotToken != "" {
		logStr = strings.ReplaceAll(logStr, config.Telegram.BotToken, "[REDACTED]")
	}

	// 将处理后的字符串转换回字节
	cleanP := []byte(logStr)

	// 写入控制台
	os.Stdout.Write(cleanP)

	// 写入文件时移除颜色代码
	cleanP = removeColorCodes(cleanP)
	if logFile != nil {
		return logFile.Write(cleanP)
	}

	return len(cleanP), nil
}

// removeColorCodes 移除ANSI颜色代码
func removeColorCodes(p []byte) []byte {
	// ANSI 颜色代码通常以 `\033[` 开头，以 `m` 结尾
	re := regexp.MustCompile("\033\\[[0-9;]*m")
	return re.ReplaceAll(p, []byte(""))
}

// 定义颜色常量
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// 定义颜色列表，用于随机选择
var colors = []string{ColorRed, ColorGreen, ColorYellow, ColorBlue, ColorCyan}

// ========= 1. 全局常量和配置 =========

// TEST_URL 是用于测试代理的 URL
const TEST_URL = "http://api.ipify.org"

// GEOIP_DB_URL 是 GeoIP 数据库的下载地址
const GEOIP_DB_URL = "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download/GeoLite2-Country.mmdb"

// GEOIP_DB_PATH 是 GeoIP 数据库的本地路径
const GEOIP_DB_PATH = "GeoLite2-Country.mmdb"

// 默认测速文件地址
const DEFAULT_SPEED_TEST_URL = "https://speed.cloudflare.com/__down?bytes=100000000"

// 可修改的测速地址变量
var SpeedTestURL = DEFAULT_SPEED_TEST_URL

const SPEED_TEST_MIN_SIZE = 100000000

var (
	// OUTPUT_FILES 定义了输出文件的名称
	OUTPUT_FILES = map[string]string{
		"socks5_auth":      "socks5_auth.txt",
		"socks5_noauth":    "socks5_noauth.txt",
		"socks4_auth":      "socks4_auth.txt",
		"socks4_noauth":    "socks4_noauth.txt",
		"http":             "http.txt",
		"https":            "https.txt",
		"socks5_auth_tg":   "socks5_auth_tg.txt",
		"socks5_noauth_tg": "socks5_noauth_tg.txt",
		"socks5_csv":       "socks5.csv",
	}

	// COUNTRY_CODE_TO_NAME 存储国家代码到中文名的映射
	COUNTRY_CODE_TO_NAME = map[string]string{
		"AF": "阿富汗", "AL": "阿尔巴尼亚", "DZ": "阿尔及利亚", "AS": "美属萨摩亚", "AD": "安道尔",
		"AO": "安哥拉", "AI": "安圭拉", "AQ": "南极洲", "AG": "安提瓜和巴布达", "AR": "阿根廷",
		"AM": "亚美尼亚", "AW": "阿鲁巴", "AU": "澳大利亚", "AT": "奥地利", "AZ": "阿塞拜疆",
		"BS": "巴哈马", "BH": "巴林", "BD": "孟加拉国", "BB": "巴巴多斯", "BY": "白俄罗斯",
		"BE": "比利时", "BZ": "伯利兹", "BM": "百慕大", "BT": "不丹", "BO": "玻利维亚",
		"BA": "波斯尼亚和黑塞哥维那", "BW": "博茨瓦纳", "BR": "巴西", "IO": "英属印度洋领地",
		"VG": "英属维尔京群岛", "BN": "文莱", "BG": "保加利亚", "BF": "布基纳法索", "BI": "布隆迪",
		"KH": "柬埔寨", "CM": "喀麦隆", "CA": "加拿大", "CV": "佛得角", "KY": "开曼群岛",
		"CF": "中非共和国", "TD": "乍得", "CL": "智利", "CN": "中国", "CX": "圣诞岛",
		"CC": "科科斯群岛", "CO": "哥伦比亚", "KM": "科摩罗", "CK": "库克群岛", "CR": "哥斯达黎加",
		"CI": "科特迪瓦", "HR": "克罗地亚", "CU": "古巴", "CY": "塞浦路斯", "CZ": "捷克共和国",
		"CD": "刚果民主共和国", "DK": "丹麦", "DJ": "吉布提", "DM": "多米尼克", "DO": "多米尼加共和国",
		"TL": "东帝汶", "EC": "厄瓜多尔", "EG": "埃及", "SV": "萨尔瓦多", "GQ": "赤道几内亚",
		"ER": "厄立特里亚", "EE": "爱沙尼亚", "ET": "埃塞俄比亚", "FK": "福克兰群岛", "FO": "法罗群岛",
		"FJ": "斐济", "FI": "芬兰", "FR": "法国", "GF": "法属圭亚那", "PF": "法属波利尼西亚",
		"TF": "法属南部领地", "GA": "加蓬", "GM": "冈比亚", "GE": "格鲁吉亚", "DE": "德国",
		"GH": "加纳", "GI": "直布罗陀", "GR": "希腊", "GL": "格陵兰", "GD": "格林纳达",
		"GP": "瓜德罗普", "GU": "关岛", "GT": "危地马拉", "GG": "根西岛", "GN": "几内亚",
		"GW": "几内亚比绍", "GY": "圭亚那", "HT": "海地", "VA": "梵蒂冈", "HN": "洪都拉斯",
		"HK": "香港", "HU": "匈牙利", "IS": "冰岛", "IN": "印度", "ID": "印度尼西亚",
		"IR": "伊朗", "IQ": "伊拉克", "IE": "爱尔兰", "IM": "马恩岛", "IL": "以色列",
		"IT": "意大利", "JM": "牙买加", "JP": "日本", "JE": "泽西岛", "JO": "约旦",
		"KZ": "哈萨克斯坦", "KE": "肯尼亚", "KI": "基里巴斯", "XK": "科索沃", "KW": "科威特",
		"KG": "吉尔吉斯斯坦", "LA": "老挝", "LV": "拉脱维亚", "LB": "黎巴嫩", "LS": "莱索托",
		"LR": "利比里亚", "LY": "利比亚", "LI": "列支敦士登", "LT": "立陶宛", "LU": "卢森堡",
		"MO": "澳门", "MK": "北马其顿", "MG": "马达加斯加", "MW": "马拉维", "MY": "马来西亚",
		"MV": "马尔代夫", "ML": "马里", "MT": "马耳他", "MH": "马绍尔群岛", "MQ": "马提尼克",
		"MR": "毛里塔尼亚", "MU": "毛里求斯", "YT": "马约特", "MX": "墨西哥", "FM": "密克罗尼西亚",
		"MD": "摩尔多瓦", "MC": "摩纳哥", "MN": "蒙古", "ME": "黑山", "MS": "蒙特塞拉特",
		"MA": "摩洛哥", "MZ": "莫桑比克", "MM": "缅甸", "NA": "纳米比亚", "NR": "瑙鲁",
		"NP": "尼泊尔", "NL": "荷兰", "NC": "新喀里多尼亚", "NZ": "新西兰", "NI": "尼加拉瓜",
		"NE": "尼日尔", "NG": "尼日利亚", "NU": "纽埃", "NF": "诺福克岛", "KP": "朝鲜",
		"MP": "北马里亚纳群岛", "NO": "挪威", "OM": "阿曼", "PK": "巴基斯坦", "PW": "帕劳",
		"PS": "巴勒斯坦", "PA": "巴拿马", "PG": "巴布亚新几内亚", "PY": "巴拉圭", "PE": "秘鲁",
		"PH": "菲律宾", "PN": "皮特凯恩群岛", "PL": "波兰", "PT": "葡萄牙", "PR": "波多黎各",
		"QA": "卡塔尔", "CG": "刚果共和国", "RE": "留尼汪", "RO": "罗马尼亚", "RU": "俄罗斯",
		"RW": "卢旺达", "BL": "圣巴泰勒米", "SH": "圣赫勒拿", "KN": "圣基茨和内维斯", "LC": "圣卢西亚",
		"MF": "法属圣马丁", "PM": "圣皮埃尔和密克隆", "VC": "圣文森特和格林纳丁斯", "WS": "萨摩亚",
		"SM": "圣马力诺", "ST": "圣多美和普林西比", "SA": "沙特阿拉伯", "SN": "塞内加尔",
		"RS": "塞尔维亚", "SC": "塞舌尔", "SL": "塞拉利昂", "SG": "新加坡", "SX": "荷属圣马丁",
		"SK": "斯洛伐克", "SI": "斯洛文尼亚", "SB": "所罗门群岛", "SO": "索马里", "ZA": "南非",
		"GS": "南乔治亚岛和南桑威奇群岛", "KR": "韩国", "SS": "南苏丹", "ES": "西班牙",
		"LK": "斯里兰卡", "SD": "苏丹", "SR": "苏里南", "SJ": "斯瓦尔巴群岛和扬马延",
		"SZ": "斯威士兰", "SE": "瑞典", "CH": "瑞士", "SY": "叙利亚", "TW": "台湾",
		"TJ": "塔吉克斯坦", "TZ": "坦桑尼亚", "TH": "泰国", "TG": "多哥", "TK": "托克劳",
		"TO": "汤加", "TT": "特立尼达和多巴哥", "TN": "突尼斯", "TR": "土耳其", "TM": "土库曼斯坦",
		"TC": "特克斯和凯科斯群岛", "TV": "图瓦卢", "UG": "乌干达", "UA": "乌克兰",
		"AE": "阿拉伯联合酋长国", "GB": "英国", "US": "美国", "UY": "乌拉圭",
		"UZ": "乌兹别克斯坦", "VU": "瓦努阿图", "VE": "委内瑞拉", "VN": "越南",
		"WF": "瓦利斯和富图纳", "EH": "西撒哈拉", "YE": "也门", "ZM": "赞比亚", "ZW": "津巴布韦",
		"UNKNOWN": "未知",
	}

	// COUNTRY_FLAG_MAP 存储国家代码到国旗表情的映射
	COUNTRY_FLAG_MAP = map[string]string{
		"AD": "🇦🇩", "AE": "🇦🇪", "AF": "🇦🇫", "AG": "🇦🇬", "AI": "🇦🇮", "AL": "🇦🇱", "AM": "🇦🇲", "AO": "🇦🇴",
		"AQ": "🇦🇶", "AR": "🇦🇷", "AS": "🇦🇸", "AT": "🇦🇹", "AU": "🇦🇺", "AW": "🇦🇼", "AX": "🇦🇽", "AZ": "🇦🇿",
		"BA": "🇧🇦", "BB": "🇧🇧", "BD": "🇧🇩", "BE": "🇧🇪", "BF": "🇧🇫", "BG": "🇧🇬", "BH": "🇧🇭", "BI": "🇧🇮",
		"BJ": "🇧🇯", "BL": "🇧🇱", "BM": "🇧🇲", "BN": "🇧🇳", "BO": "🇧🇴", "BQ": "🇧🇶", "BR": "🇧🇷", "BS": "🇧🇸",
		"BT": "🇧🇹", "BV": "🇧🇻", "BW": "🇧🇼", "BY": "🇧🇾", "BZ": "🇧🇿", "CA": "🇨🇦", "CC": "🇨🇨", "CD": "🇨🇩",
		"CF": "🇨🇫", "CG": "🇨🇬", "CH": "🇨🇭", "CI": "🇨🇮", "CK": "🇨🇰", "CL": "🇨🇱", "CM": "🇨🇲", "CN": "🇨🇳",
		"CO": "🇨🇴", "CR": "🇨🇷", "CU": "🇨🇺", "CV": "🇨🇻", "CW": "🇨🇼", "CX": "🇨🇽", "CY": "🇨🇾", "CZ": "🇨🇿",
		"DE": "🇩🇪", "DJ": "🇩🇯", "DK": "🇩🇰", "DM": "🇩🇲", "DO": "🇩🇴", "DZ": "🇩🇿", "EC": "🇪🇨", "EE": "🇪🇪",
		"EG": "🇪🇬", "EH": "🇪🇭", "ER": "🇪🇷", "ES": "🇪🇸", "ET": "🇪🇹", "FI": "🇫🇮", "FJ": "🇫🇯", "FK": "🇫🇰",
		"FM": "🇫🇲", "FO": "🇫🇴", "FR": "🇫🇷", "GA": "🇬🇦", "GB": "🇬🇧", "GD": "🇬🇩", "GE": "🇬🇪", "GF": "🇬🇫",
		"GG": "🇬🇬", "GH": "🇬🇭", "GI": "🇬🇮", "GL": "🇬🇱", "GM": "🇬🇲", "GN": "🇬🇳", "GP": "🇬🇵", "GQ": "🇬🇶",
		"GR": "🇬🇷", "GS": "🇬🇸", "GT": "🇬🇹", "GU": "🇬🇺", "GW": "🇬🇼", "GY": "🇬🇾", "HK": "🇭🇰", "HM": "🇭🇲",
		"HN": "🇭🇳", "HR": "🇭🇷", "HT": "🇭🇹", "HU": "🇭🇺", "ID": "🇮🇩", "IE": "🇮🇪", "IL": "🇮🇱", "IM": "🇮🇲",
		"IN": "🇮🇳", "IO": "🇮🇴", "IQ": "🇮🇶", "IR": "🇮🇷", "IS": "🇮🇸", "IT": "🇮🇹", "JE": "🇯🇪", "JM": "🇯🇲",
		"JO": "🇯🇴", "JP": "🇯🇵", "KE": "🇰🇪", "KG": "🇰🇬", "KH": "🇰🇭", "KI": "🇰🇮", "KM": "🇰🇲", "KN": "🇰🇳",
		"KP": "🇰🇵", "KR": "🇰🇷", "KW": "🇰🇼", "KY": "🇰🇾", "KZ": "🇰🇿", "LA": "🇱🇦", "LB": "🇱🇧", "LC": "🇱🇨",
		"LI": "🇱🇮", "LK": "🇱🇰", "LR": "🇱🇷", "LS": "🇱🇸", "LT": "🇱🇹", "LU": "🇱🇺", "LV": "🇱🇻", "LY": "🇱🇾",
		"MA": "🇲🇦", "MC": "🇲🇨", "MD": "🇲🇩", "ME": "🇲🇪", "MF": "🇲🇫", "MG": "🇲🇬", "MH": "🇲🇷", "MK": "🇲🇰",
		"ML": "🇲🇱", "MM": "🇲🇲", "MN": "🇲🇳", "MO": "🇲🇴", "MP": "🇲🇵", "MQ": "🇲🇶", "MR": "🇲🇷", "MS": "🇲🇸",
		"MT": "🇲🇹", "MU": "🇲🇺", "MV": "🇲🇻", "MW": "🇲🇼", "MX": "🇲🇽", "MY": "🇲🇾", "MZ": "🇲🇿", "NA": "🇳🇦",
		"NC": "🇳🇨", "NE": "🇳🇪", "NF": "🇳🇫", "NG": "🇳🇬", "NI": "🇳🇮", "NL": "🇳🇱", "NO": "🇳🇴", "NP": "🇳🇵",
		"NR": "🇳🇷", "NU": "🇳🇺", "NZ": "🇳🇿", "OM": "🇴🇲", "PA": "🇵🇦", "PE": "🇵🇪", "PF": "🇵🇫", "PG": "🇵🇬",
		"PH": "🇵🇭", "PK": "🇵🇰", "PL": "🇵🇱", "PM": "🇵🇲", "PN": "🇵🇳", "PR": "🇵🇷", "PS": "🇵🇸", "PT": "🇵🇹",
		"PW": "🇵🇼", "PY": "🇵🇾", "QA": "🇶🇦", "RE": "🇷🇪", "RO": "🇷🇴", "RS": "🇷🇸", "RU": "🇷🇺", "RW": "🇷🇼",
		"SA": "🇸🇦", "SB": "🇸🇧", "SC": "🇸🇨", "SD": "🇸🇩", "SE": "🇸🇪", "SG": "🇸🇬", "SH": "🇸🇭", "SI": "🇸🇮",
		"SJ": "🇸🇯", "SK": "🇸🇰", "SL": "🇸🇱", "SM": "🇸🇲", "SN": "🇸🇳", "SO": "🇸🇴", "SR": "🇸🇷", "SS": "🇸🇸",
		"ST": "🇸🇹", "SV": "🇸🇻", "SX": "🇸🇽", "SY": "🇸🇾", "SZ": "🇸🇿", "TC": "🇹🇨", "TD": "🇹🇩", "TF": "🇹🇫",
		"TG": "🇹🇬", "TH": "🇹🇭", "TJ": "🇹🇯", "TK": "🇹🇰", "TL": "🇹🇱", "TM": "🇹🇲", "TN": "🇹🇳", "TO": "🇹🇴",
		"TR": "🇹🇷", "TT": "🇹🇹", "TV": "🇹🇻", "TW": "🇹🇼", "TZ": "🇹🇿", "UA": "🇺🇦", "UG": "🇺🇬", "UM": "🇺🇲",
		"US": "🇺🇸", "UY": "🇺🇾", "UZ": "🇺🇿", "VA": "🇻🇦", "VC": "🇻🇨", "VE": "🇻🇪", "VG": "🇻🇬", "VI": "🇻🇮",
		"VN": "🇻🇳", "VU": "🇻🇺", "WF": "🇼🇫", "WS": "🇼🇸", "XK": "🇽🇰", "YE": "🇾🇪", "YT": "🇾🇹", "ZA": "🇿🇦",
		"ZM": "🇿🇲", "ZW": "🇿🇼", "UNKNOWN": "🌐",
	}

	// FAILURE_REASON_MAP 定义失败原因的规范化映射
	FAILURE_REASON_MAP = map[string]string{
		"EOF":                            "连接中断",
		"read: connection reset by peer": "连接被重置",
		"context deadline exceeded":      "操作超时",
		"connect: connection refused":    "连接被拒",
		"dial tcp":                      "连接失败 (TCP)",
		"lookup":                        "DNS解析失败",
		"no route to host":              "主机不可达",
		"connection was reset":           "连接重置",
		"i/o timeout":                   "I/O超时",
		"tls: handshake failure":         "TLS握手失败",
		"tls: internal error":            "TLS内部错误",
		"connection abort":              "连接异常中断",
		"proxy connect tcp":             "代理连接失败",
		"Bad Request":                   "请求错误 (Bad Request)",
	}
)

// ProxyInfo 结构体用于存储解析出的代理信息
type ProxyInfo struct {
	URL      string
	Protocol string
	Reason   string // 仅用于初始解析阶段
}

// ProxyResult 结构体用于存储检测结果
type ProxyResult struct {
	URL      string
	Protocol string
	Latency  float64
	Success  bool
	IP       string
	Reason   string
	DownloadSpeed float64
}

// Telegram API 响应结构体
type telegramAPIResponse struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

// GeoIPManager 结构体用于封装 GeoIP Reader 和缓存
type GeoIPManager struct {
	reader *geoip2.Reader
	mu     sync.RWMutex
	cache  map[string]string
}

// geoIPManager 是 GeoIPManager 的全局实例
var geoIPManager = &GeoIPManager{
	cache: make(map[string]string),
}

// telegramClientCache 缓存一个已验证的 Telegram 客户端，避免重复验证
var (
	telegramClientCache *http.Client
	clientCacheMutex    sync.Mutex
)

// 计算字符串在终端中的显示宽度，中文字符占2个宽度，表情符号等也占2个宽度
func getStringDisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		if utf8.RuneLen(r) > 1 {
			width += 2 // 中文字符等双宽字符
		} else {
			width += 1 // 英文、数字等单宽字符
		}
	}
	return width
}

// DrawCenteredTitleBox 绘制居中标题框
func DrawCenteredTitleBox(title string, width int) {
	// 1. 移除 ANSI 颜色代码，以获取纯文本
	cleanTitle := removeColorCodes([]byte(title))

	// 2. 使用新函数，准确计算标题的显示宽度
	titleDisplayWidth := getStringDisplayWidth(string(cleanTitle))

	// 3. 定义标题框内部的总显示宽度（标题 + 左右各2个空格）
	innerBoxWidth := titleDisplayWidth + 4

	// 4. 标题框总宽度 = 内部宽度 + 左右边框
	boxTotalWidth := innerBoxWidth + 2

	// 如果标题框总宽度超出终端宽度，则不居中
	if boxTotalWidth >= width {
		fmt.Println()
		fmt.Println("╔" + strings.Repeat("═", innerBoxWidth) + "╗")
		fmt.Println("║  " + title + "  ║")
		fmt.Println("╚" + strings.Repeat("═", innerBoxWidth) + "╝")
		fmt.Println()
		return
	}

	// 5. 计算左右两边的填充空格数，以实现居中
	padding := (width - boxTotalWidth) / 2
	paddingStr := strings.Repeat(" ", padding)

	// 6. 构建标题框的每一行，确保长度完全一致
	topBorder := paddingStr + "╔" + strings.Repeat("═", innerBoxWidth) + "╗"
	titleLine := paddingStr + "║  " + title + "  ║"
	bottomBorder := paddingStr + "╚" + strings.Repeat("═", innerBoxWidth) + "╝"

	fmt.Println()
	fmt.Println(topBorder)
	fmt.Println(titleLine)
	fmt.Println(bottomBorder)
	fmt.Println()
}

// loadConfig 读取配置文件并打印美化后的日志
func loadConfig(configPath string) error {
	cfg, err := ini.Load(configPath)
	if err != nil {
		return fmt.Errorf("❌ 无法加载配置文件: %w", err)
	}

	err = cfg.MapTo(&config)
	if err != nil {
		return fmt.Errorf("❌ 无法映射配置到结构体: %w", err)
	}

	proxyStr := cfg.Section("settings").Key("preset_proxy").String()
	if proxyStr != "" {
		config.Settings.PresetProxy = strings.Split(proxyStr, ",")
	}

	// 获取终端宽度
	width, _, err := terminal.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80 // 默认宽度
	}

	// 使用新的函数来绘制标题框，并将标题文本设置为黄色
	DrawCenteredTitleBox(ColorYellow+"  s5 代 理 检 测 工 具 v1.0.3  "+ColorReset, width)

	// 打印美化后的配置加载成功提示
	log.Println(ColorGreen + "✅ 配置加载成功！" + ColorReset)
	if config.Telegram.BotToken != "" && config.Telegram.ChatID != "" {
		log.Println(ColorCyan + "- Telegram 机器人已就绪。" + ColorReset)
	} else {
		log.Println(ColorYellow + "- Telegram 配置不完整，将跳过通知。" + ColorReset)
	}

	if len(config.Settings.PresetProxy) > 0 {
		log.Printf(ColorCyan+"- 已加载 %d 个预设代理。\n", len(config.Settings.PresetProxy))
	} else {
		log.Println(ColorYellow + "- 没有预设代理，将使用直连方式下载GeoIP数据库。" + ColorReset)
	}

	log.Printf(ColorCyan+"- 输入目录 %s\n", config.Settings.FdipDir)
	log.Printf(ColorCyan+"- 输出目录 %s\n", config.Settings.OutputDir)
	log.Printf(ColorCyan+"- 测速地址 %s\n", config.Settings.SpeedTestURL)
	log.Printf(ColorCyan+"- 检测超时设置为 %d 秒，\n", config.Settings.CheckTimeout)
	log.Printf(ColorCyan+"- 最大并发数 %d。\n" + ColorReset, config.Settings.MaxConcurrent)
	log.Println(ColorCyan + "------------------------------------------" + ColorReset)

	return nil
}

// ========= 2. GeoIP 数据库处理函数 =========

// downloadGeoIPDatabase 尝试下载 GeoIP 数据库文件
func downloadGeoIPDatabase(dbPath string) bool {
	log.Printf("ℹ️ 正在下载 GeoIP 数据库到: %s\n", dbPath)

	for _, proxyURL := range config.Settings.PresetProxy {
		log.Printf("⏳ 尝试通过预设代理 %s 下载 GeoIP 数据库...\n", proxyURL)

		transport, err := createTransportWithProxy(proxyURL)
		if err != nil {
			log.Printf("❌ 创建代理 transport 失败: %v\n", err)
			continue
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		}

		resp, err := client.Get(GEOIP_DB_URL)
		if err != nil {
			log.Printf("❌ 通过代理 %s 下载 GeoIP 数据库失败: %v\n", proxyURL, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("❌ 下载 GeoIP 数据库 HTTP 状态码非 200: %d\n", resp.StatusCode)
			continue
		}

		outFile, err := os.Create(dbPath)
		if err != nil {
			log.Printf("❌ 创建 GeoIP 数据库文件失败: %v\n", err)
			continue
		}
		defer outFile.Close()

		_, err = io.Copy(outFile, resp.Body)
		if err != nil {
			log.Printf("❌ 写入 GeoIP 数据库文件失败: %v\n", err)
			continue
		}

		if isGeoIPFileValid(dbPath) {
			log.Printf("🟢 成功通过代理 %s 下载 GeoIP 数据库到 %s\n", proxyURL, dbPath)
			return true
		} else {
			log.Printf("⚠️ 通过代理 %s 下载的 GeoIP 数据库无效，删除文件。\n", proxyURL)
			os.Remove(dbPath)
		}
	}

	log.Printf("❌ 无法下载 GeoIP 数据库到 %s，将尝试直连...\n", dbPath)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(GEOIP_DB_URL)
	if err != nil {
		log.Printf("❌ 直连下载 GeoIP 数据库失败: %v\n", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ 直连下载 GeoIP 数据库 HTTP 状态码非 200: %d\n", resp.StatusCode)
		return false
	}

	outFile, err := os.Create(dbPath)
	if err != nil {
		log.Printf("❌ 直连创建 GeoIP 数据库文件失败: %v\n", err)
		return false
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		log.Printf("❌ 直连写入 GeoIP 数据库文件失败: %v\n", err)
		return false
	}
	if isGeoIPFileValid(dbPath) {
		log.Printf("🟢 成功通过直连下载 GeoIP 数据库到 %s\n", dbPath)
		return true
	}
	log.Printf("❌ 直连下载的 GeoIP 数据库无效，删除文件。\n")
	os.Remove(dbPath)
	return false
}

// isGeoIPFileValid 验证 GeoIP 数据库文件是否有效且未过期
func isGeoIPFileValid(filePath string) bool {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false
	}
	fileInfo, _ := os.Stat(filePath)
	if fileInfo.Size() < 1024*1024 { // 1MB
		log.Printf("⚠️ GeoIP 数据库文件 %s 过小，可能无效。\n", filePath)
		return false
	}
	mtime := fileInfo.ModTime()
	ageDays := time.Since(mtime).Hours() / 24
	if ageDays > 30 {
		log.Printf("⚠️ GeoIP 数据库文件 %s 已超过 30 天 (%.1f 天)，建议更新。\n", filePath, ageDays)
	}

	reader, err := geoip2.Open(filePath)
	if err != nil {
		log.Printf("❌ GeoIP 数据库文件 %s 验证失败: %v\n", filePath, err)
		return false
	}
	defer reader.Close()

	ip := net.ParseIP("8.8.8.8")
	if ip == nil {
		return false
	}
	country, err := reader.Country(ip)
	if err != nil {
		log.Printf("❌ GeoIP 数据库测试失败: %v\n", err)
		return false
	}
	if country.Country.IsoCode != "" {
		log.Printf("✅ GeoIP 数据库测试成功，IP %s -> %s\n", ip, country.Country.IsoCode)
		return true
	}
	log.Printf("❌ GeoIP 数据库测试失败，IP %s 无国家代码。\n", ip)
	return false
}

// initGeoIPReader 初始化 GeoIP 数据库读取器
func initGeoIPReader() {
	log.Println("----------- GeoIP 数据库初始化 -----------")
	if _, err := os.Stat(GEOIP_DB_PATH); err == nil && isGeoIPFileValid(GEOIP_DB_PATH) {
		log.Printf("✅ 本地 GeoIP 数据库已存在且有效: %s\n", GEOIP_DB_PATH)
	} else {
		if err == nil {
			log.Printf("⚠️ 本地 GeoIP 数据库无效或已过期: %s，将尝试重新下载。\n", GEOIP_DB_PATH)
			os.Remove(GEOIP_DB_PATH)
		} else {
			log.Printf("ℹ️ 本地 GeoIP 数据库不存在: %s，尝试下载最新文件。\n", GEOIP_DB_PATH)
		}

		if !downloadGeoIPDatabase(GEOIP_DB_PATH) {
			log.Printf("❌ 下载 GeoIP 数据库失败，地理位置查询将不可用。\n")
			log.Println("------------------------------------------")
			return
		}
	}

	reader, err := geoip2.Open(GEOIP_DB_PATH)
	if err != nil {
		log.Printf("❌ GeoIP 数据库加载失败: %v。地理位置查询将不可用。\n", err)
		log.Println("------------------------------------------")
		return
	}
	geoIPManager.reader = reader
	log.Println("✅ GeoIP 数据库加载成功。")
	log.Println("------------------------------------------")
}

// closeGeoIPReader 关闭 GeoIP 数据库读取器
func closeGeoIPReader() {
	if geoIPManager.reader != nil {
		if err := geoIPManager.reader.Close(); err != nil {
			log.Printf("⚠️ 关闭 GeoIP 数据库失败: %v\n", err)
		} else {
			log.Println("ℹ️ GeoIP 数据库已关闭。")
		}
		geoIPManager.reader = nil
	}
}

// getCountryFromIPBatch 批量查询 IP 的国家代码
func getCountryFromIPBatch(ips []string) map[string]string {
	results := make(map[string]string)
	if geoIPManager.reader == nil {
		log.Printf("⚠️ GeoIP 数据库未加载，无法查询国家信息。\n")
		for _, ip := range ips {
			results[ip] = "UNKNOWN"
		}
		return results
	}

	for _, ipStr := range ips {
		geoIPManager.mu.RLock()
		if code, ok := geoIPManager.cache[ipStr]; ok {
			results[ipStr] = code
			geoIPManager.mu.RUnlock()
			continue
		}
		geoIPManager.mu.RUnlock()

		ip := net.ParseIP(ipStr)
		if ip == nil {
			results[ipStr] = "UNKNOWN"
			continue
		}
		country, err := geoIPManager.reader.Country(ip)
		if err != nil {
			results[ipStr] = "UNKNOWN"
			continue
		}
		countryCode := country.Country.IsoCode
		if _, ok := COUNTRY_FLAG_MAP[countryCode]; !ok {
			countryCode = "UNKNOWN"
		}
		results[ipStr] = countryCode

		geoIPManager.mu.Lock()
		geoIPManager.cache[ipStr] = countryCode
		geoIPManager.mu.Unlock()
	}
	return results
}

// ========= 3. 代理解析和测试函数 =========

// extractProxiesFromFile 从指定目录的txt文件中提取代理
func extractProxiesFromFile(dir string, maxGoRoutines int) chan *ProxyInfo {
	proxiesChan := make(chan *ProxyInfo, maxGoRoutines*2)
	// 使用 regexp.MustCompile 来编译正则表达式
	// 这个正则表达式专门用于匹配 ip:port | user:pass |... 的格式
	reAuthSocks5 := regexp.MustCompile(`^([\d.]+):(\d+)\s*\|\s*([^|]*?):([^|]*?)\s*\|.*$`)

	go func() {
		defer close(proxiesChan)
		files, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("[错误] 读取目录 %s 失败: %v\n", dir, err)
			return
		}

		var wg sync.WaitGroup
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(strings.ToLower(file.Name()), ".txt") {
				wg.Add(1)
				go func(fileName string) {
					defer wg.Done()
					filePath := filepath.Join(dir, fileName)
					f, err := os.Open(filePath)
					if err != nil {
						log.Printf("[错误] 打开文件 %s 失败: %v\n", filePath, err)
						return
					}
					defer f.Close()

					scanner := bufio.NewScanner(f)
					for scanner.Scan() {
						line := strings.TrimSpace(scanner.Text())
						if line == "" || strings.HasPrefix(line, "#") {
							continue
						}

						// 新格式：如果包含逗号，取逗号前部分作为URL
						if strings.Contains(line, ",") {
							parts := strings.Split(line, ",")
							proxyURLStr := strings.TrimSpace(parts[0])
							parsedURL, err := url.Parse(proxyURLStr)
							if err == nil && parsedURL.Scheme != "" && parsedURL.Host != "" {
								protocol := parsedURL.Scheme
								if strings.HasPrefix(protocol, "socks5") && parsedURL.User != nil {
									protocol = "socks5_auth"
								} else if strings.HasPrefix(protocol, "socks5") && parsedURL.User == nil {
									protocol = "socks5_noauth"
								}
								proxiesChan <- &ProxyInfo{
									URL:      parsedURL.String(),
									Protocol: protocol,
								}
								continue
							}
						}

						// 1. 尝试以 `#` 分割并解析为 URL 格式 (socks5://user:pass@host:port#...)
						proxyURLStr := strings.SplitN(line, "#", 2)[0]
						parsedURL, err := url.Parse(proxyURLStr)
						if err == nil && parsedURL.Scheme != "" && parsedURL.Host != "" {
							protocol := parsedURL.Scheme
							if strings.HasPrefix(protocol, "socks5") && parsedURL.User != nil {
								protocol = "socks5_auth"
							} else if strings.HasPrefix(protocol, "socks5") && parsedURL.User == nil {
								protocol = "socks5_noauth"
							}
							proxiesChan <- &ProxyInfo{
								URL:      parsedURL.String(),
								Protocol: protocol,
							}
							continue
						}

						// 2. 尝试用正则表达式匹配旧格式：ip:port | user:pass |...
						if matches := reAuthSocks5.FindStringSubmatch(line); len(matches) == 5 {
							ip, port, username, password := matches[1], matches[2], matches[3], matches[4]
							pi := &ProxyInfo{
								URL: fmt.Sprintf("socks5://%s:%s@%s:%s",
									url.QueryEscape(username), url.QueryEscape(password), ip, port),
								Protocol: "socks5_auth",
							}
							proxiesChan <- pi
							continue
						}

						// 3. 尝试解析其他格式（例如 ip:port:protocol |...）
						parts := strings.SplitN(line, "|", 2)
						proxyStr := strings.TrimSpace(parts[0])

						proxyParts := strings.Split(proxyStr, ":")
						if len(proxyParts) >= 3 {
							protocol := strings.ToLower(proxyParts[len(proxyParts)-1])
							ip := strings.Join(proxyParts[:len(proxyParts)-2], ":")
							port := proxyParts[len(proxyParts)-2]

							switch protocol {
							case "socks5", "socks4", "http", "https":
								// 构造 URL
								u := &url.URL{Scheme: protocol, Host: fmt.Sprintf("%s:%s", ip, port)}

								proxiesChan <- &ProxyInfo{
									URL:      u.String(),
									Protocol: protocol,
								}
								continue
							}
						}

						// 如果所有尝试都失败，记录警告
						log.Printf("[警告] 无法解析代理行: %s\n", line)
					}
				}(file.Name())
			}
		}
		wg.Wait()
	}()
	return proxiesChan
}

// testProxy 测试单个代理的有效性
func testProxy(ctx context.Context, proxyInfo *ProxyInfo) ProxyResult {
	start := time.Now()

	// 解析 URL
	_, err := url.Parse(proxyInfo.URL)
	if err != nil {
		return ProxyResult{URL: proxyInfo.URL, Success: false, Reason: "URL解析失败"}
	}

	// 创建代理客户端
	var transport *http.Transport
	transport, err = createTransportWithProxy(proxyInfo.URL)
	if err != nil {
		return ProxyResult{URL: proxyInfo.URL, Success: false, Reason: "代理创建失败"}
	}

	// 使用配置中的超时值，如果配置未指定，则使用默认 30 秒
	timeout := 30 // 默认超时 30 秒
	if config.Settings.CheckTimeout > 0 {
		timeout = config.Settings.CheckTimeout
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(timeout) * time.Second, // 使用动态超时值
	}

	// 创建请求并发送
	req, err := http.NewRequestWithContext(ctx, "GET", TEST_URL, nil)
	if err != nil {
		return ProxyResult{URL: proxyInfo.URL, Success: false, Reason: "请求创建失败"}
	}

	resp, err := client.Do(req)
	if err != nil {
		return ProxyResult{URL: proxyInfo.URL, Success: false, Reason: "网络错误"}
	}
	defer resp.Body.Close()

	// 检查 HTTP 响应状态码
	if resp.StatusCode != http.StatusOK {
		return ProxyResult{URL: proxyInfo.URL, Success: false, Reason: fmt.Sprintf("HTTP 错误: %d", resp.StatusCode)}
	}

	// 计算延迟
	latency := time.Since(start).Seconds() * 1000 // 转换为毫秒
	body, _ := io.ReadAll(resp.Body)

	// 初始化结果
	result := ProxyResult{
		URL:      proxyInfo.URL,
		Protocol: proxyInfo.Protocol,
		Latency:  latency,
		Success:  true,
		IP:       strings.TrimSpace(string(body)),
		Reason:   "",
	}

	// 为下载测试设置更高的超时
	client.Timeout = 30 * time.Second

	// 开始下载速度测试
	downloadStart := time.Now()
	req, err = http.NewRequestWithContext(ctx, "GET", SpeedTestURL, nil)
	if err != nil {
		result.Reason = fmt.Sprintf("下载请求创建失败: %v", err)
		return result
	}

	resp, err = client.Do(req)
	if err != nil {
		result.Reason = fmt.Sprintf("下载失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	// 检查下载响应状态码
	if resp.StatusCode != http.StatusOK {
		result.Reason = fmt.Sprintf("下载 HTTP 错误: %d", resp.StatusCode)
		return result
	}

	// 计算下载速度
	n, err := io.Copy(io.Discard, resp.Body)
	downloadDuration := time.Since(downloadStart).Seconds()
	if n > 0 && downloadDuration > 0 {
		result.DownloadSpeed = float64(n) / (1024 * 1024) / downloadDuration
	} else {
		result.DownloadSpeed = 0
	}

	// 处理下载错误
	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			result.Reason = fmt.Sprintf("超时 (已下载 %.2f MB)", float64(n)/(1024*1024))
		} else {
			result.Reason = fmt.Sprintf("下载错误: %v (已下载 %.2f MB)", err, float64(n)/(1024*1024))
		}
	} else if n < SPEED_TEST_MIN_SIZE {
		result.Reason = fmt.Sprintf("下载大小不足: %d 字节", n)
	}

	return result
}

// createTransportWithProxy 创建一个带代理的 http.Transport
func createTransportWithProxy(proxyURL string) (*http.Transport, error) {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}

	switch parsedURL.Scheme {
	case "http", "https":
		return &http.Transport{
			Proxy:       http.ProxyURL(parsedURL),
			DialContext: dialer.DialContext,
		}, nil
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			password, _ := parsedURL.User.Password()
			auth = &proxy.Auth{User: parsedURL.User.Username(), Password: password}
		}

		socks5Dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, dialer)
		if err != nil {
			return nil, err
		}

		return &http.Transport{
			DialContext: socks5Dialer.(proxy.ContextDialer).DialContext,
		}, nil
	case "socks4":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{User: parsedURL.User.Username()}
		}

		socks4Dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, dialer)
		if err != nil {
			return nil, err
		}

		return &http.Transport{
			DialContext: socks4Dialer.(proxy.ContextDialer).DialContext,
		}, nil
	default:
		return nil, fmt.Errorf("不支持的协议: %s", parsedURL.Scheme)
	}
}

// runProxyTests 并发测试代理
func runProxyTests(proxiesChan chan *ProxyInfo) chan ProxyResult {
	resultsChan := make(chan ProxyResult)
	var wg sync.WaitGroup

	// 启动 worker goroutine
	for i := 0; i < config.Settings.MaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range proxiesChan {
				result := testProxy(context.Background(), p)
				resultsChan <- result
			}
		}()
	}

	// 启动一个 goroutine 来关闭结果通道
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	return resultsChan
}

// ========= 4. Telegram 通知函数 =========

// escapeMarkdownV2 对字符串进行转义以符合MarkdownV2规范
func escapeMarkdownV2(text string) string {
	var escaped bytes.Buffer
	for _, r := range text {
		switch r {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
			escaped.WriteRune('\\')
			escaped.WriteRune(r)
		default:
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}

// createTelegramClientWithProxy 创建一个带代理的 HTTP 客户端用于 Telegram 通信
func createTelegramClientWithProxy(proxyURL string) (*http.Client, error) {
	var transport *http.Transport
	var err error

	if proxyURL == "" {
		transport = &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
			}).DialContext,
		}
	} else {
		transport, err = createTransportWithProxy(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("代理验证失败: %v", err)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", config.Telegram.BotToken)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("代理验证失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("代理验证失败，HTTP 状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}
	return client, nil
}

// getTelegramClient 获取一个可用的 Telegram 客户端，并进行缓存
func getTelegramClient() *http.Client {
	clientCacheMutex.Lock()
	defer clientCacheMutex.Unlock()

	// 如果缓存中已有有效的客户端，直接返回
	if telegramClientCache != nil {
		return telegramClientCache
	}

	var client *http.Client
	var err error

	// 尝试通过预设代理连接 Telegram
	for _, proxyURL := range config.Settings.PresetProxy {
		log.Printf("⏳ 尝试代理 %s...\n", proxyURL)
		client, err = createTelegramClientWithProxy(proxyURL)
		if err == nil {
			log.Printf("🟢 成功通过代理建立 Telegram 会话。\n")
			telegramClientCache = client // 缓存成功的客户端
			return client
		}
		// 简洁显示：仅代理 URL + 失败原因，不打印详细 err（详细 err 已记录到文件日志）
		log.Printf("❌ 代理 %s 验证失败\n", proxyURL)
	}

	// 如果所有代理都失败，尝试直连
	log.Println("⏳ 尝试直连 Telegram API...")
	client, err = createTelegramClientWithProxy("")
	if err == nil {
		log.Println("✅ 直连 Telegram API 成功。")
		telegramClientCache = client
		return client
	}

	log.Println("❌ 直连 Telegram API 失败，所有连接方式均失败。")
	return nil
}

// sendTelegramMessage 发送 Telegram 消息
func sendTelegramMessage(message string) bool {
	if config.Telegram.BotToken == "" || config.Telegram.ChatID == "" {
		log.Println("❌ Telegram 配置不完整，跳过消息发送")
		return false
	}

	client := getTelegramClient()
	if client == nil {
		log.Println("❌ 无法建立 Telegram 连接，跳过消息发送")
		return false
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", config.Telegram.BotToken)
	payload := map[string]string{
		"chat_id":    config.Telegram.ChatID,
		"text":       message,
		"parse_mode": "MarkdownV2",
	}

	jsonPayload, _ := json.Marshal(payload)
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		log.Println("❌ Telegram 消息发送失败")
		// 清除缓存客户端
		clientCacheMutex.Lock()
		telegramClientCache = nil
		clientCacheMutex.Unlock()
		return false
	}
	defer resp.Body.Close()

	var apiResp telegramAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil || !apiResp.Ok {
		log.Println("❌ Telegram 消息发送失败: API 错误")
		// 清除缓存客户端
		clientCacheMutex.Lock()
		telegramClientCache = nil
		clientCacheMutex.Unlock()
		return false
	}

	log.Println("✅ Telegram 消息发送成功！")
	return true
}

// sendTelegramFile 发送 Telegram 文件
func sendTelegramFile(filePath string) bool {
	if config.Telegram.BotToken == "" || config.Telegram.ChatID == "" {
		log.Println("❌ 未配置 TELEGRAM_BOT_TOKEN 或 TELEGRAM_CHAT_ID，跳过 Telegram 文件通知")
		return false
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("ℹ️ 文件 %s 不存在，跳过推送。\n", filepath.Base(filePath))
		return false
	}
	fileInfo, _ := os.Stat(filePath)
	if fileInfo.Size() == 0 {
		log.Printf("ℹ️ 文件 %s 不存在或为空，跳过推送。\n", filepath.Base(filePath))
		os.Remove(filePath)
		return false
	}

	client := getTelegramClient()
	if client == nil {
		log.Println("❌ 无法建立网络连接，跳过 Telegram 文件发送。")
		return false
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", config.Telegram.BotToken)

	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("❌ 无法打开文件 %s: %v\n", filePath, err)
		return false
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		log.Printf("❌ 创建 multipart 表单文件失败: %v\n", err)
		return false
	}
	_, err = io.Copy(part, file)
	if err != nil {
		log.Printf("❌ 复制文件到表单失败: %v\n", err)
		return false
	}
	writer.WriteField("chat_id", config.Telegram.ChatID)
	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		log.Printf("❌ 创建 HTTP 请求失败: %v\n", err)
		return false
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("❌ 文件 %s 发送失败\n", filePath)
		// 如果发送失败，清除缓存客户端
		clientCacheMutex.Lock()
		telegramClientCache = nil
		clientCacheMutex.Unlock()
		log.Println("⚠️ Telegram 客户端已失效，已清除缓存，下次将重新验证。")
		return false
	}
	defer resp.Body.Close()

	var apiResp telegramAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil || !apiResp.Ok {
		log.Printf("❌ Telegram API 错误: %s\n", apiResp.Description)
		// 如果API返回错误，清除缓存客户端
		clientCacheMutex.Lock()
		telegramClientCache = nil
		clientCacheMutex.Unlock()
		return false
	}

	log.Printf("✅ 文件 %s 已成功推送。\n", filepath.Base(filePath))
	return true
}

// ========= 5. 写入结果文件函数 =========

// writeValidProxies 将有效的代理列表写入相应的输出文件
func writeValidProxies(validProxies []ProxyResult) {
	if _, err := os.Stat(config.Settings.OutputDir); os.IsNotExist(err) {
		os.Mkdir(config.Settings.OutputDir, 0755)
	}

	groupedProxies := make(map[string][]ProxyResult)
	for _, proxy := range validProxies {
		key := strings.Replace(proxy.Protocol, "socks5h", "socks5", 1)
		groupedProxies[key] = append(groupedProxies[key], proxy)

		// 为 socks5 代理单独处理 Telegram 格式
		if key == "socks5_auth" || key == "socks5_noauth" {
			groupedProxies[key+"_tg"] = append(groupedProxies[key+"_tg"], proxy)
		}
	}

	// 对每个代理组进行下载速度降序排序
	for key, proxies := range groupedProxies {
		// 排序：按下载速度降序排列
		sort.Slice(proxies, func(i, j int) bool {
			return proxies[i].DownloadSpeed > proxies[j].DownloadSpeed
		})
		groupedProxies[key] = proxies
	}

	// 生成输出文件
	for key, file := range OUTPUT_FILES {
		// 忽略处理 CSV 文件，单独处理
		if key == "socks5_csv" {
			continue
		}

		proxies := groupedProxies[key]
		fullPath := filepath.Join(config.Settings.OutputDir, file)

		if len(proxies) > 0 {
			outFile, err := os.Create(fullPath)
			if err != nil {
				log.Printf("❌ 写入文件 %s 失败: %v\n", fullPath, err)
				continue
			}
			defer outFile.Close()

			for _, p := range proxies {
				countryCode := p.IP
				flag := COUNTRY_FLAG_MAP[countryCode]
				if flag == "" {
					flag = COUNTRY_FLAG_MAP["UNKNOWN"]
				}
				countryName := COUNTRY_CODE_TO_NAME[countryCode]

				// 检查是否为 TG 格式文件，需要转换 SOCKS5 为 Telegram MTProto 链接
				var proxyURL string
				if strings.HasSuffix(key, "_tg") {
					// 解析原始 SOCKS5 URL
					parsedURL, err := url.Parse(p.URL)
					if err != nil {
						log.Printf("⚠️ 解析 TG 代理 URL 失败: %s，继续使用原格式\n", p.URL)
						proxyURL = p.URL
					} else {
						// 提取组件
						server := parsedURL.Hostname()
						port := parsedURL.Port()
						userInfo := parsedURL.User
						username := ""
						password := ""
						if userInfo != nil {
							username = userInfo.Username()
							password, _ = userInfo.Password()
						}
						// 构建 Telegram 代理 URL
						proxyURL = fmt.Sprintf("https://t.me/socks?server=%s&port=%s&user=%s&pass=%s", server, port, username, password)
					}
				} else {
					proxyURL = p.URL
				}

				// 生成每条代理的输出，加入下载速度信息
				line := fmt.Sprintf("%s, 延迟: %.2fms, 速度: %.2fMB/s, 国家: %s %s\n", proxyURL, p.Latency, p.DownloadSpeed, flag, countryName)
				outFile.WriteString(line)
			}
			log.Printf("💾 已写入 %d 条代理到文件: %s\n", len(proxies), fullPath)
		} else {
			if _, err := os.Stat(fullPath); err == nil {
				os.Remove(fullPath)
				log.Printf("🗑️ 已删除空文件: %s\n", fullPath)
			} else {
				log.Printf("ℹ️ 文件 %s 不存在或为空，跳过写入。\n", fullPath)
			}
		}
	}

	// 写入 CSV 文件（按下载速度降序排列）
	var socks5Proxies []ProxyResult
	for _, p := range validProxies {
		socks5Proxies = append(socks5Proxies, p)
	}
	sort.Slice(socks5Proxies, func(i, j int) bool {
		return socks5Proxies[i].DownloadSpeed > socks5Proxies[j].DownloadSpeed
	})
	if len(socks5Proxies) > 0 {
		fullPath := filepath.Join(config.Settings.OutputDir, "socks5.csv")
		outFile, err := os.Create(fullPath)
		if err != nil {
			log.Printf("❌ 写入文件 %s 失败: %v\n", fullPath, err)
			return
		}
		defer outFile.Close()
		writer := bufio.NewWriter(outFile)
		writer.WriteString("代理协议,用户名,密码,IP,端口,国家,网络延迟,下载速度\n")
		for _, p := range socks5Proxies {
			parsedURL, _ := url.Parse(p.URL)
			ip := parsedURL.Hostname()
			port := parsedURL.Port()
			username := ""
			password := ""
			if parsedURL.User != nil {
				username = parsedURL.User.Username()
				password, _ = parsedURL.User.Password()
			}
			countryCode := p.IP
			countryName := COUNTRY_CODE_TO_NAME[countryCode]
			protocol := p.Protocol
			if strings.Contains(protocol, "socks5") {
				protocol = "socks5"
			}
			line := fmt.Sprintf("%s,%s,%s,%s,%s,%s,%.2f ms,%.2f MB/s\n", protocol, username, password, ip, port, countryName, p.Latency, p.DownloadSpeed)
			writer.WriteString(line)
		}
		writer.Flush()
		log.Printf("💾 已写入 %d 条代理到文件: %s\n", len(socks5Proxies), fullPath)
	}
}


// runCheck 是代理检测的核心逻辑
func runCheck() {
	log.Println(ColorGreen + "**🚀 代理检测工具启动**" + ColorReset)
	log.Println(ColorCyan + "------------------------------------------" + ColorReset)

	start := time.Now()

	if config.Telegram.BotToken != "" && config.Telegram.ChatID != "" {
		message := "*🚀 代理检测工具启动*"
		maxRetries := 3
		for i := 0; i < maxRetries; i++ {
			if sendTelegramMessage(message) {
				break
			}
			if i < maxRetries-1 {
				log.Printf("❌ Telegram 启动消息发送失败 (第 %d 次)，5秒后重试...", i+1)
				time.Sleep(5 * time.Second)
			} else {
				log.Println("❌ Telegram 启动消息发送失败，但程序将继续运行。")
			}
		}
	} else {
		log.Println(ColorYellow + "❌ 未配置 Telegram Bot Token 或 Chat ID，跳过 Telegram 通知。" + ColorReset)
	}

	initGeoIPReader()
	defer closeGeoIPReader()

	fdipPath := filepath.Join(".", config.Settings.FdipDir)
	if _, err := os.Stat(fdipPath); os.IsNotExist(err) {
		log.Printf(ColorRed+"❌ 目录不存在: %s\n"+ColorReset, fdipPath)
		sendTelegramMessage(escapeMarkdownV2("❌ 错误: 目录 `"+config.Settings.FdipDir+"` 不存在"))
		return
	}

	proxiesChan := extractProxiesFromFile(fdipPath, config.Settings.MaxConcurrent)

	// 在 extractProxiesFromFile 完成后，将所有代理收集到一个切片中，以便后续处理
	var allProxies []*ProxyInfo
	for p := range proxiesChan {
		allProxies = append(allProxies, p)
	}

	if len(allProxies) == 0 {
		log.Println(ColorYellow + "⚠️ 未提取到任何代理，退出" + ColorReset)
		sendTelegramMessage(escapeMarkdownV2("⚠️ *代理检测完成*\n没有提取到任何代理"))
		return
	}

	log.Println(ColorCyan + "⏳ 正在异步检测代理有效性，请稍候..." + ColorReset)

	// 将代理分发到测试通道
	testProxiesChan := make(chan *ProxyInfo, config.Settings.MaxConcurrent)
	go func() {
		defer close(testProxiesChan)
		for _, p := range allProxies {
			testProxiesChan <- p
		}
	}()

	// runProxyTests 现在返回一个结果通道
	resultsChan := runProxyTests(testProxiesChan)

	var validProxies []ProxyResult
	failedProxiesStats := make(map[string]int)
	ipsToQuery := make(map[string]struct{})

	// 实时处理结果
	for result := range resultsChan {
		if result.Success {
			// 过滤低速代理
			if result.DownloadSpeed > 0.1 {
				// 打印可用代理的实时信息
				if result.Reason != "" {
					log.Printf(ColorGreen+"✅ 可用: %s | 延迟: %.2fms | 速度: %.2fMB | 原因: %s\n"+ColorReset, result.URL, result.Latency, result.DownloadSpeed, result.Reason)
				} else {
					log.Printf(ColorGreen+"✅ 可用: %s | 延迟: %.2fms | 速度: %.2fMB\n"+ColorReset, result.URL, result.Latency, result.DownloadSpeed)
				}

				validProxies = append(validProxies, result)
				if result.IP != "" {
					ipsToQuery[result.IP] = struct{}{}
				}
			}
		} else {
			// 打印失败代理的实时信息
			reason := result.Reason
			normalizedReason := "其他错误"
			for key, val := range FAILURE_REASON_MAP {
				if strings.Contains(reason, key) {
					normalizedReason = val
					break
				}
			}
			reHTTPStatus := regexp.MustCompile(`HTTP Status: (\d+)`)
			if matches := reHTTPStatus.FindStringSubmatch(reason); len(matches) == 2 {
				statusCode, _ := strconv.Atoi(matches[1])
				if statusCode >= 400 && statusCode < 500 {
					normalizedReason = fmt.Sprintf("客户端错误 (%d)", statusCode)
				} else if statusCode >= 500 && statusCode < 600 {
					normalizedReason = fmt.Sprintf("服务器错误 (%d)", statusCode)
				} else {
					normalizedReason = fmt.Sprintf("HTTP 状态 (%d)", statusCode)
				}
			}
			log.Printf(ColorRed+"❌ 失败: %s | 原因: %s\n"+ColorReset, result.URL, normalizedReason)
			failedProxiesStats[normalizedReason]++
		}
	}

	log.Println(ColorCyan + "\n🎉 代理检测完成，正在生成报告..." + ColorReset)

	if len(validProxies) == 0 {
		log.Println(ColorYellow + "⚠️ 没有检测到可用代理" + ColorReset)
		sendTelegramMessage(escapeMarkdownV2("⚠️ *代理检测完成*\n没有检测到任何可用代理"))
		return
	}

	ips := make([]string, 0, len(ipsToQuery))
	for ip := range ipsToQuery {
		ips = append(ips, ip)
	}
	countryCodesMap := getCountryFromIPBatch(ips)

	for i := range validProxies {
		if countryCode, ok := countryCodesMap[validProxies[i].IP]; ok {
			validProxies[i].IP = countryCode
		} else {
			validProxies[i].IP = "UNKNOWN"
		}
	}

	log.Println(ColorCyan + "\n💾 正在写入结果文件..." + ColorReset)
	writeValidProxies(validProxies)

	totalValidCount := len(validProxies)
	protocolDistribution := make(map[string]int)
	countryDistribution := make(map[string]int)
	var latencies []float64
	var downloadSpeeds []float64

	for _, p := range validProxies {
		protoKey := p.Protocol
		if strings.HasPrefix(protoKey, "socks5") {
			protoKey += "_tg" // 为了统计 telegram 格式的数量
		}
		protocolDistribution[protoKey]++
		countryDistribution[p.IP]++
		latencies = append(latencies, p.Latency)
		downloadSpeeds = append(downloadSpeeds, p.DownloadSpeed)
	}

	minLatency, maxLatency, avgLatency := 0.0, 0.0, 0.0
	if len(latencies) > 0 {
		sort.Float64s(latencies)
		minLatency = latencies[0]
		maxLatency = latencies[len(latencies)-1]
		var sum float64
		for _, l := range latencies {
			sum += l
		}
		avgLatency = sum / float64(len(latencies))
	}

	minSpeed, maxSpeed, avgSpeed := 0.0, 0.0, 0.0
	if len(downloadSpeeds) > 0 {
		sort.Float64s(downloadSpeeds)
		minSpeed = downloadSpeeds[0]
		maxSpeed = downloadSpeeds[len(downloadSpeeds)-1]
		var sum float64
		for _, s := range downloadSpeeds {
			sum += s
		}
		avgSpeed = sum / float64(len(downloadSpeeds))
	}

	log.Println(ColorGreen + "\n🎉 代理检测报告" + ColorReset)
	log.Printf("⏰ 耗时: %.2f 秒\n", time.Since(start).Seconds())
	log.Printf("✅ 有效代理: %d 个\n", totalValidCount)
	if len(protocolDistribution) > 0 {
		log.Println(ColorBlue + "\n🌐 协议分布:" + ColorReset)
		var sortedProtocols []string
		for proto := range protocolDistribution {
			sortedProtocols = append(sortedProtocols, proto)
		}
		sort.Strings(sortedProtocols)
		for _, proto := range sortedProtocols {
			log.Printf("  - %s: %d 个\n", proto, protocolDistribution[proto])
		}
	}
	if len(countryDistribution) > 0 {
		log.Println(ColorBlue + "\n🌍 国家分布:" + ColorReset)
		var sortedCountries []string
		for country := range countryDistribution {
			sortedCountries = append(sortedCountries, country)
		}
		sort.Strings(sortedCountries)
		for _, countryCode := range sortedCountries {
			flag := COUNTRY_FLAG_MAP[countryCode]
			countryName := COUNTRY_CODE_TO_NAME[countryCode]
			log.Printf("  - %s %s (%s): %d 个\n", flag, countryName, countryCode, countryDistribution[countryCode])
		}
	}
	if len(latencies) > 0 {
		log.Println(ColorBlue + "\n📈 延迟统计:" + ColorReset)
		log.Printf("  - 均值: %.2fms\n", avgLatency)
		log.Printf("  - 最低: %.2fms\n", minLatency)
		log.Printf("  - 最高: %.2fms\n", maxLatency)
	}
	if len(downloadSpeeds) > 0 {
		log.Println(ColorBlue + "\n📊 下载速度统计:" + ColorReset)
		log.Printf("  - 均值: %.2f MB/s\n", avgSpeed)
		log.Printf("  - 最低: %.2f MB/s\n", minSpeed)
		log.Printf("  - 最高: %.2f MB/s\n", maxSpeed)
	}
	if len(failedProxiesStats) > 0 {
		log.Println(ColorRed + "\n⚠️ 检测失败原因:" + ColorReset)
		var reasons []string
		for reason := range failedProxiesStats {
			reasons = append(reasons, reason)
		}
		sort.Slice(reasons, func(i, j int) bool {
			return failedProxiesStats[reasons[i]] > failedProxiesStats[reasons[j]]
		})
		for _, reason := range reasons {
			log.Printf("  - %s: %d 个\n", reason, failedProxiesStats[reason])
		}
	}

	var messageParts []string
	messageParts = append(messageParts, "*🎉 代理检测报告*")
	messageParts = append(messageParts, fmt.Sprintf("⏰ 耗时: `%.2f` 秒", time.Since(start).Seconds()))
	messageParts = append(messageParts, fmt.Sprintf("✅ 有效代理: `%d` 个", totalValidCount))

	if len(protocolDistribution) > 0 {
		messageParts = append(messageParts, "\n*🌐 协议分布*:")
		var sortedProtocols []string
		for proto := range protocolDistribution {
			sortedProtocols = append(sortedProtocols, proto)
		}
		sort.Strings(sortedProtocols)
		for _, proto := range sortedProtocols {
			messageParts = append(messageParts, fmt.Sprintf("  - `%s`: `%d` 个", proto, protocolDistribution[proto]))
		}
	}
	if len(countryDistribution) > 0 {
		messageParts = append(messageParts, "\n*🌍 国家分布*:")
		var sortedCountries []string
		for country := range countryDistribution {
			sortedCountries = append(sortedCountries, country)
		}
		sort.Strings(sortedCountries)
		for _, countryCode := range sortedCountries {
			flag := COUNTRY_FLAG_MAP[countryCode]
			countryName := COUNTRY_CODE_TO_NAME[countryCode]
			messageParts = append(messageParts, fmt.Sprintf("  - %s %s: `%d` 个", flag, countryName, countryDistribution[countryCode]))
		}
	}
	if len(latencies) > 0 {
		messageParts = append(messageParts, "\n*📈 延迟统计*:")
		messageParts = append(messageParts, fmt.Sprintf("  - 均值: `%.2f`ms", avgLatency))
		messageParts = append(messageParts, fmt.Sprintf("  - 最低: `%.2f`ms", minLatency))
		messageParts = append(messageParts, fmt.Sprintf("  - 最高: `%.2f`ms", maxLatency))
	}
	if len(downloadSpeeds) > 0 {
		messageParts = append(messageParts, "\n*📊 下载速度统计*:")
		messageParts = append(messageParts, fmt.Sprintf("  - 均值: `%.2f` MB/s", avgSpeed))
		messageParts = append(messageParts, fmt.Sprintf("  - 最低: `%.2f` MB/s", minSpeed))
		messageParts = append(messageParts, fmt.Sprintf("  - 最高: `%.2f` MB/s", maxSpeed))
	}
	if len(failedProxiesStats) > 0 {
		messageParts = append(messageParts, "\n*⚠️ 检测失败原因*:")
		var reasons []string
		for reason := range failedProxiesStats {
			reasons = append(reasons, reason)
		}
		sort.Slice(reasons, func(i, j int) bool {
			return failedProxiesStats[reasons[i]] > failedProxiesStats[reasons[j]]
		})
		for _, reason := range reasons {
			messageParts = append(messageParts, fmt.Sprintf("  - `%s`: `%d` 个", reason, failedProxiesStats[reason]))
		}
	}

	finalTelegramMessage := strings.Join(messageParts, "\n")
	finalTelegramMessage = escapeMarkdownV2(finalTelegramMessage)
	finalTelegramMessage = strings.ReplaceAll(finalTelegramMessage, "\\*", "*")
	finalTelegramMessage = strings.ReplaceAll(finalTelegramMessage, "\\`", "`")

	if config.Telegram.BotToken != "" && config.Telegram.ChatID != "" {
		maxRetries := 3
		for i := 0; i < maxRetries; i++ {
			if sendTelegramMessage(finalTelegramMessage) {
				log.Println("✅ 检测报告推送成功")
				break
			}
			if i < maxRetries-1 {
				log.Printf("❌ 检测报告推送失败 (第 %d 次)，5秒后重试...", i+1)
				time.Sleep(5 * time.Second)
			} else {
				log.Println("❌ 检测报告推送失败，但程序将继续运行。")
			}
		}
	}

	log.Println(ColorCyan + "\n📤 正在推送所有输出文件..." + ColorReset)
	csvFile := ""
	for _, filePath := range OUTPUT_FILES {
		if filePath == "socks5.csv" {
			csvFile = filepath.Join(config.Settings.OutputDir, filePath)
			continue
		}
		fullPath := filepath.Join(config.Settings.OutputDir, filePath)
		sendTelegramFile(fullPath)
	}
	if csvFile != "" {
		sendTelegramFile(csvFile)
	}

	// 修复后的方案：参考启动消息，直接发送粗体字符串，不经过 escapeMarkdownV2
	if config.Telegram.BotToken != "" && config.Telegram.ChatID != "" {
		sendTelegramMessage("*🎉 程序运行结束*")
	}

	// 修改：将终端打印的结束消息也显示为粗体
	log.Println(ColorGreen + "\033[1m🎉 程序运行结束！\033[0m" + ColorReset)
}
// ========= 5. 菜单和主函数 =========

// showMenu 显示主菜单并处理用户输入
func showMenu() {
	for {
		fmt.Println(ColorYellow + "\n--- 请选择一个操作 ---" + ColorReset)
		fmt.Println("1. 🚀 " + ColorGreen + "开始代理检测" + ColorReset)
		fmt.Println("2. 🌐 " + ColorBlue + "更新 GeoIP 数据库" + ColorReset)
		fmt.Println("3. ❌ " + ColorRed + "退出" + ColorReset)
		fmt.Print("请输入您的选择 (1/2/3): ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(input)

		switch choice {
		case "1":
			runCheck()
		case "2":
			downloadGeoIPDatabase(GEOIP_DB_PATH)
		case "3":
			fmt.Println("👋 退出程序。")
			return
		default:
			fmt.Println(ColorRed + "⚠️ 无效的选择，请重新输入。" + ColorReset)
		}
	}
}

// ========= 5.5. 交互式设置 (新添加) =========

// promptUser 是一个辅助函数，用于显示提示并获取用户输入
// 它支持显示默认值，并在用户直接按 Enter 键时使用该默认值
func promptUser(reader *bufio.Reader, promptText string, defaultValue string) string {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", promptText, defaultValue)
	} else {
		fmt.Printf("%s: ", promptText)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}
	return input
}

// interactiveSetup 引导用户完成首次配置并保存到 config.ini
func interactiveSetup(configPath string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println(ColorYellow + "\n--- 首次运行配置 ---" + ColorReset)
	fmt.Println("未找到配置文件，请按照提示输入配置。")
	fmt.Println("按 [Enter] 键可使用方括号 [] 中的默认值。")

	// 创建一个新的、空的 ini 配置对象
	cfg := ini.Empty()

	// [telegram] section
	fmt.Println(ColorCyan + "\n[1. Telegram 配置 (可选)]" + ColorReset)
	botToken := promptUser(reader, "请输入 Telegram Bot Token (留空跳过)", "")
	chatID := promptUser(reader, "请输入 Telegram Chat ID (留空跳过)", "")
	cfg.Section("telegram").Key("bot_token").SetValue(botToken)
	cfg.Section("telegram").Key("chat_id").SetValue(chatID)

	// [settings] section
	fmt.Println(ColorCyan + "\n[2. Settings 配置 (必填)]" + ColorReset)
	fdipDir := promptUser(reader, "请输入代理文件输入目录", "fdip")
	outputDir := promptUser(reader, "请输入结果文件输出目录", "output")
	checkTimeoutStr := promptUser(reader, "请输入检测超时 (秒)", "10")
	maxConcurrentStr := promptUser(reader, "请输入最大并发数", "100")
	speedTestURL := promptUser(reader, "请输入测速文件地址", DEFAULT_SPEED_TEST_URL) // 使用已定义的常量
	presetProxy := promptUser(reader, "请输入预设代理 (SOCKS5/HTTP, 多个用逗号分隔, 留空跳过)", "")

	// 将值设置到 ini 对象中
	cfg.Section("settings").Key("fdip_dir").SetValue(fdipDir)
	cfg.Section("settings").Key("output_dir").SetValue(outputDir)
	cfg.Section("settings").Key("check_timeout").SetValue(checkTimeoutStr)
	cfg.Section("settings").Key("max_concurrent").SetValue(maxConcurrentStr)
	cfg.Section("settings").Key("speed_test_url").SetValue(speedTestURL)
	cfg.Section("settings").Key("preset_proxy").SetValue(presetProxy)

	// 保存配置文件
	err := cfg.SaveTo(configPath)
	if err != nil {
		return fmt.Errorf("❌ 无法保存配置文件到 %s: %w", configPath, err)
	}

	fmt.Println(ColorGreen + "✅ 配置已成功保存到 " + configPath + ColorReset)
	fmt.Println("下次启动将自动加载此配置。")

	// 重新加载配置到全局变量，以便本次运行继续
	// 注意：这里我们让 main 函数中的 loadConfig 负责加载
	return nil
}

// ========= 6. 主函数和辅助功能 =========

func main() {
    // 设置日志格式
    log.SetFlags(0)
    var err error
    logFile, err = os.OpenFile("check_log.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
    if err != nil {
        log.Fatalf("❌ 无法打开日志文件: %v", err)
    }
    defer logFile.Close()
    log.SetOutput(&LogWriter{})

    // 命令行参数定义
    showHelp := flag.Bool("h", false, "显示帮助信息")
    configPath := flag.String("c", "config.ini", "指定配置文件路径（默认 config.ini）")
    speedURL := flag.String("s", "", "自定义测速文件地址（可选）")
    inputDir := flag.String("i", "", "指定代理输入目录（可选，覆盖配置文件 settings.fdip_dir）")
    outputDir := flag.String("o", "", "指定输出目录（可选，覆盖配置文件 settings.output_dir）")
    flag.Parse()

    // 处理帮助选项
    if *showHelp {
        fmt.Println("代理检测工具 v1.0.3 使用帮助：")
        fmt.Println(" -h 显示帮助信息")
        fmt.Println(" -c <路径> 指定配置文件路径（默认 config.ini）")
        fmt.Println(" -i <目录> 指定代理输入目录（可选，覆盖配置文件）")
        fmt.Println(" -o <目录> 指定输出目录（可选，覆盖配置文件）")
        fmt.Println(" -s <URL> 指定测速文件地址（可选）")
        fmt.Println()
        return
    }

    // --- 新增的逻辑 ---
    // 1. 检查配置文件是否存在
    if _, err := os.Stat(*configPath); os.IsNotExist(err) {
        // 2. 如果不存在，运行交互式设置
        if setupErr := interactiveSetup(*configPath); setupErr != nil {
            log.Fatalf("❌ 交互式设置失败: %v", setupErr)
        }
    }
    // --- 逻辑结束 ---

    // 3. 加载配置文件（无论是已存在的还是刚刚创建的）
    if err := loadConfig(*configPath); err != nil {
        log.Fatalf("❌ 配置加载失败: %v", err)
    }

    // (main 函数的剩余部分保持不变)
    // 设置测速地址（优先级：命令行 > ini 配置 > 默认）
    if *speedURL != "" {
        SpeedTestURL = *speedURL
    } else if config.Settings.SpeedTestURL != "" {
        // 确保 URL 是完整的（添加 https:// 前缀如果缺少）
        fullURL := config.Settings.SpeedTestURL
        if !strings.HasPrefix(fullURL, "http://") && !strings.HasPrefix(fullURL, "https://") {
             fullURL = "https://" + fullURL
        }
        SpeedTestURL = fullURL
    }
    // 优先使用命令行指定目录
    if *inputDir != "" {
        config.Settings.FdipDir = *inputDir
    }
    if *outputDir != "" {
        config.Settings.OutputDir = *outputDir
    }
    // 默认参数修复
    if config.Settings.CheckTimeout <= 0 {
         config.Settings.CheckTimeout = 10
        log.Printf("⚠️ 未设置检测超时，使用默认值: %d 秒\n", config.Settings.CheckTimeout)
    }
    if config.Settings.MaxConcurrent <= 0 {
        config.Settings.MaxConcurrent = 100
        log.Printf("⚠️ 未设置最大并发数，使用默认值: %d\n", config.Settings.MaxConcurrent)
    }
    if config.Settings.FdipDir == "" {
        config.Settings.FdipDir = "fdip"
        log.Printf("⚠️ 未设置代理目录，使用默认值: %s\n", config.Settings.FdipDir)
    }
    if config.Settings.OutputDir == "" {
         config.Settings.OutputDir = "output"
        log.Printf("⚠️ 未设置输出目录，使用默认值: %s\n", config.Settings.OutputDir)
    }
    showMenu()
}

