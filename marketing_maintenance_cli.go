package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/go-redis/redis/v8"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// This command inherits the host's deployment credentials. It never listens on
// a socket or accepts a database URL/OS identity from its input document.
func runMarketingMaintenanceCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("marketing-maintenance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("input", "", "private JSON file, or - for trusted stdin")
	execute := flags.Bool("execute", false, "apply the evidenced maintenance operation (default: inspect only)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *path == "" {
		fmt.Fprintln(stderr, "Use marketing-maintenance --input FILE [--execute]")
		return 2
	}
	if *execute && os.Getenv("MARKETING_MAINTENANCE_ENABLED") != "true" {
		fmt.Fprintln(stderr, "Local maintenance execution is not enabled")
		return 2
	}
	reader := stdin
	if *path != "-" {
		// O_NONBLOCK prevents a malicious FIFO from hanging before descriptor checks.
		fd, err := syscall.Open(*path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if err != nil {
			fmt.Fprintln(stderr, "Cannot open private maintenance input")
			return 2
		}
		file := os.NewFile(uintptr(fd), "maintenance-input")
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			fmt.Fprintln(stderr, "Cannot inspect maintenance input")
			return 2
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ok || int(stat.Uid) != os.Geteuid() {
			fmt.Fprintln(stderr, "Input must be a regular file owned by the operator with no group or other access")
			return 2
		}
		reader = file
	}
	const maxInput = 256 * 1024
	raw, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil || len(raw) > maxInput {
		fmt.Fprintln(stderr, "Invalid maintenance input size")
		return 2
	}
	var input service.MarketingMaintenanceRequest
	if err := common.Unmarshal(raw, &input); err != nil {
		fmt.Fprintln(stderr, "Invalid maintenance JSON")
		return 2
	}
	gin.DefaultWriter = stderr
	gin.DefaultErrorWriter = stderr
	*common.LogDir = ""
	common.InitEnv()
	if err := model.OpenMarketingMaintenanceDatabase(); err != nil {
		fmt.Fprintln(stderr, "Unable to read the existing maintenance database")
		return 1
	}
	sqlDB, err := model.DB.DB()
	if err != nil {
		fmt.Fprintln(stderr, "Unable to access maintenance database")
		return 1
	}
	defer sqlDB.Close()
	common.RedisEnabled = false
	if *execute && os.Getenv("REDIS_CONN_STRING") != "" {
		options, err := redis.ParseURL(os.Getenv("REDIS_CONN_STRING"))
		if err != nil {
			fmt.Fprintln(stderr, "Invalid deployment cache configuration")
			return 1
		}
		common.RDB = redis.NewClient(options)
		defer common.RDB.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = common.RDB.Ping(ctx).Err()
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, "Cannot reach the configured shared cache; no operation was executed")
			return 1
		}
		common.RedisEnabled = true
	}
	result, err := service.RunMarketingMaintenance(context.Background(), input, "local-maintenance:uid="+strconv.Itoa(os.Getuid())+":euid="+strconv.Itoa(os.Geteuid()), *execute)
	if err != nil {
		fmt.Fprintln(stderr, "Maintenance was not completed:", err)
		return 1
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		fmt.Fprintln(stderr, "Cannot encode maintenance result")
		return 1
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		return 1
	}
	return 0
}
