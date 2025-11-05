#!/bin/bash

# =====================================
# 脚本信息
# =====================================
# 程序名称: s5-‎proxy-checker_v1.0.3
# 描述: 此脚本用于在 Linux 中交叉编译 Go 程序，支持多个平台和架构。
# 作者: [您的名称或留空]
# 版本: 1.2 (Linux 版，已优化 CGO 并增强代理提示)
# 最后更新: 2025-11-05
# =====================================

# 设置程序名称和源文件
PROG_NAME="s5-‎proxy-checker_v1.0.3"
SOURCE_FILE="aigo.go"

# 随机生成终端颜色（ANSI 转义码，前景色）
COLOR_CODES=("32" "36" "31" "35" "33" "37")  # 绿色、青色、红色、紫色、黄色、白色
RANDOM_INDEX=$((RANDOM % ${#COLOR_CODES[@]}))
COLOR="\e[${COLOR_CODES[$RANDOM_INDEX]}m"
RESET="\e[0m"

# 提示设置代理
echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}检测到可能需要代理来下载依赖。如果您的网络需要，请输入代理 URL。${RESET}"
echo -e "${COLOR}（例如: http://127.0.0.1:8080 或 socks5://127.0.0.1:7890）${RESET}"
read -p "代理 URL (留空跳过): " PROXY_URL
if [ ! -z "$PROXY_URL" ]; then
    echo -e "${COLOR}[INFO] 已设置临时代理: $PROXY_URL 🔒${RESET}"
else
    echo -e "${COLOR}[INFO] 未设置代理，继续...${RESET}"
fi
echo

# 输出欢迎信息
echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}欢迎使用 $PROG_NAME 交叉编译脚本！🛠️${RESET}"
echo -e "${COLOR}支持平台: Linux, Windows, Darwin (macOS)${RESET}"
echo -e "${COLOR}支持架构: amd64, arm64, arm${RESET}"
echo -e "${COLOR}输出目录: build/${RESET}"
echo -e "${COLOR}注意: 已启用 CGO_ENABLED=0 来确保交叉编译兼容性。${RESET}"
echo -e "${COLOR}================================================${RESET}"
echo

# 提示输入通用模块名称（默认 github.com/example/ip-asn-lookup）
echo -e "${COLOR}[INFO] 请输入 Go 模块名称（建议格式: github.com/yourusername/project-name）。留空使用默认 'github.com/example/ip-asn-lookup'。${RESET}"
read -p "模块名称: " MODULE_NAME
if [ -z "$MODULE_NAME" ]; then
    MODULE_NAME="github.com/example/ip-asn-lookup"
fi
echo -e "${COLOR}[INFO] 使用模块名称: $MODULE_NAME${RESET}"
echo

# 检查 Go 环境和模块初始化
if [ ! -f "go.mod" ]; then
    echo -e "${COLOR}[INFO] 未找到 go.mod 文件，正在初始化 Go 模块... 🔄${RESET}"
    (
        # 临时设置代理仅用于此命令（支持 SOCKS5）
        if [ ! -z "$PROXY_URL" ]; then
            export http_proxy="$PROXY_URL"
            export https_proxy="$PROXY_URL"
            export HTTP_PROXY="$PROXY_URL"
            export HTTPS_PROXY="$PROXY_URL"
        fi
        go mod init "$MODULE_NAME"
    )
    if [ $? -ne 0 ]; then
        echo -e "${COLOR}[ERROR] 初始化 Go 模块失败！请检查 Go 环境。 ❌${RESET}"
        read -p "按任意键退出..." -n1 -s
        exit 1
    fi
    echo -e "${COLOR}[SUCCESS] Go 模块初始化完成。 ✅${RESET}"
    echo
fi

# 自动处理依赖（可选，但推荐）
echo -e "${COLOR}[INFO] 正在整理 Go 依赖... 🔄${RESET}"
(
    # 临时设置代理仅用于此命令（支持 SOCKS5）
    if [ ! -z "$PROXY_URL" ]; then
        export http_proxy="$PROXY_URL"
        export https_proxy="$PROXY_URL"
        export HTTP_PROXY="$PROXY_URL"
        export HTTPS_PROXY="$PROXY_URL"
    fi
    go mod tidy
)
if [ $? -ne 0 ]; then
    echo -e "${COLOR}[WARNING] Go 依赖整理失败，继续编译可能出错。 ⚠️${RESET}"
else
    echo -e "${COLOR}[SUCCESS] Go 依赖整理完成。 ✅${RESET}"
fi
echo

# 检查源文件是否存在
if [ ! -f "$SOURCE_FILE" ]; then
    echo -e "${COLOR}[ERROR] 源文件 $SOURCE_FILE 不存在！ ❌${RESET}"
    read -p "按任意键退出..." -n1 -s
    exit 1
fi

# 定义支持的目标平台
PLATFORMS=("linux/amd64" "linux/arm64" "linux/arm" "windows/amd64" "windows/arm64" "darwin/amd64" "darwin/arm64")

# 创建输出目录
mkdir -p build

# 开始编译过程
echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}[START] 开始交叉编译（共 7 个平台）... 🚀${RESET}"
echo -e "${COLOR}================================================${RESET}"
echo

SUCCESS_COUNT=0
FAIL_COUNT=0
TOTAL_PLATFORMS=7
CURRENT=0

for p in "${PLATFORMS[@]}"; do
    ((CURRENT++))
    
    # 分割平台和架构
    GOOS=$(echo "$p" | cut -d'/' -f1)
    GOARCH=$(echo "$p" | cut -d'/' -f2)
    
    # 设置输出文件名
    OUTPUT_NAME="$PROG_NAME-$GOOS-$GOARCH"
    if [ "$GOOS" == "windows" ]; then
        OUTPUT_NAME="$OUTPUT_NAME.exe"
    fi
    
    # 输出进度
    echo -e "${COLOR}[PROGRESS] $CURRENT/$TOTAL_PLATFORMS - 正在编译: $GOOS/$GOARCH → build/$OUTPUT_NAME 🛠️${RESET}"
    
    # 设置环境变量并编译（在子shell中隔离环境）
    (
        # 临时设置代理
        if [ ! -z "$PROXY_URL" ]; then
            export http_proxy="$PROXY_URL"
            export https_proxy="$PROXY_URL"
            export HTTP_PROXY="$PROXY_URL"
            export HTTPS_PROXY="$PROXY_URL"
        fi
        
        # 强制禁用 CGO 以实现可靠的交叉编译
        export CGO_ENABLED=0 
        
        export GOOS="$GOOS"
        export GOARCH="$GOARCH"
        go build -ldflags="-s -w" -o "build/$OUTPUT_NAME" "$SOURCE_FILE"
    )
    
    # 检查编译结果
    if [ $? -eq 0 ]; then
        echo -e "${COLOR}[SUCCESS] 编译成功: build/$OUTPUT_NAME ✅${RESET}"
        ((SUCCESS_COUNT++))
    else
        echo -e "${COLOR}[ERROR] 编译失败: $GOOS/$GOARCH ❌${RESET}"
        ((FAIL_COUNT++))
    fi
    echo
done

# 编译总结
echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}[SUMMARY] 编译完成！ 🎉${RESET}"
echo -e "${COLOR} - 成功: $SUCCESS_COUNT 个${RESET}"
echo -e "${COLOR} - 失败: $FAIL_COUNT 个${RESET}"
echo -e "${COLOR} - 总计: $TOTAL_PLATFORMS 个${RESET}"
echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}[FILES] build 目录下文件列表:${RESET}"
ls build
echo

# 在 Linux 中尝试打开目录（使用 xdg-open，如果可用）
if command -v xdg-open &> /dev/null; then
    echo -e "${COLOR}[INFO] 正在打开 build 目录... 📂${RESET}"
    xdg-open build
else
    echo -e "${COLOR}[WARNING] 无法自动打开目录，请手动查看。 ⚠️${RESET}"
fi

echo -e "${COLOR}================================================${RESET}"
echo -e "${COLOR}按任意键退出...${RESET}"
read -p "" -n1 -s
