package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Snipa22/core-go-lib/helpers"
	core "github.com/Snipa22/core-go-lib/milieu"
	"github.com/Snipa22/core-go-lib/milieu/middleware"
	"github.com/Snipa22/go-tari-grpc-lib/v3/nodeGRPC"
	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-xmr-lib/daemon"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/snipa22/go-tari-pool-shim/poolStratum"
	"github.com/snipa22/go-tari-pool-shim/subsystems/blockTemplateCache"
	"github.com/snipa22/go-tari-pool-shim/subsystems/config"
	"github.com/snipa22/go-tari-pool-shim/subsystems/tipDataCache"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

/*
   This project is a very light json-rpc to GRPC shim to process data into a format that nodejs-pool can use, aka:
   it takes GRPC responses and turns them directly into monerod compatible json-rpc outputs.

   This only implements a few of the many json-rpc endpoints that moneord offers.
   This implements the following functions:
   submitblock -- Complete
   getlastblockheader
   getblockheaderbyhash
   getblocktemplate -- Pending data coming from minotari_node
*/

var tariTipBlock *tari_generated.GetNewBlockResult
var tariBlockCache = make(map[string]*tari_generated.GetNewBlockResult)
var tariBlockCacheList = make([]string, 0)
var tariBlockCacheLock sync.RWMutex
var tariPoolPayoutAddress = "1215dapiKwqGxk9TAjELMf9gnH6iKM5B9gLbMBvtDSVATRtnBsKDN8bfxGECaPC1wwA8AwRLnq1Ycg28Qx71uW8pABi"
var poolMinerID []byte
var mainGRPCNode string
var isSoloMode bool
var grpcNodeList = []string{
	"135.181.112.185:18102",
	"51.91.215.198:18102",
	"51.210.222.91:18102",
	"141.94.99.110:18102",
	"184.164.76.218:18102",
	"162.218.117.106:18102",
	"162.218.117.98:18102",
	"184.164.76.210:18102",
	"15.235.227.47:18102",
	"15.235.227.59:18102",
	"15.235.228.36:18102",
}

type rpcResultError struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Error   string `json:"error"`
}

type getBlockTemplateStruct struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		WalletAddress string `json:"wallet_address"`
		ReserveSize   int    `json:"reserve_size"`
	} `json:"params"`
}

type getBlockHeaderByHashStuct struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Hash string `json:"hash"`
	} `json:"params"`
}
type submitBlockStruct struct {
	Jsonrpc string   `json:"jsonrpc"`
	ID      string   `json:"id"`
	Method  string   `json:"method"`
	Params  []string `json:"params"`
}

type getLastBlockHeaderResultWrapper struct {
	ID      string                   `json:"id"`
	Jsonrpc string                   `json:"jsonrpc"`
	Result  getLastBlockHeaderResult `json:"result"`
}

type BlockHeader struct {
	BlockSize                 int    `json:"block_size"`
	BlockWeight               int    `json:"block_weight"`
	CumulativeDifficulty      int64  `json:"cumulative_difficulty"`
	CumulativeDifficultyTop64 int    `json:"cumulative_difficulty_top64"`
	Depth                     int    `json:"depth"`
	Difficulty                int64  `json:"difficulty"`
	DifficultyTop64           int    `json:"difficulty_top64"`
	Hash                      string `json:"hash"`
	Height                    int    `json:"height"`
	LongTermWeight            int    `json:"long_term_weight"`
	MajorVersion              int    `json:"major_version"`
	MinerTxHash               string `json:"miner_tx_hash"`
	MinorVersion              int    `json:"minor_version"`
	Nonce                     int    `json:"nonce"`
	NumTxes                   int    `json:"num_txes"`
	OrphanStatus              bool   `json:"orphan_status"`
	PowHash                   string `json:"pow_hash"`
	PrevHash                  string `json:"prev_hash"`
	Reward                    int64  `json:"reward"`
	Timestamp                 int    `json:"timestamp"`
	WideCumulativeDifficulty  string `json:"wide_cumulative_difficulty"`
	WideDifficulty            string `json:"wide_difficulty"`
}
type getLastBlockHeaderResult struct {
	BlockHeader BlockHeader `json:"block_header"`
	Credits     int         `json:"credits"`
	Status      string      `json:"status"`
	TopHash     string      `json:"top_hash"`
	Untrusted   bool        `json:"untrusted"`
}

func handleGetLastBlockHeader(c *gin.Context) {
	milieu := middleware.MustGetMilieu(c)
	tipData := tipDataCache.GetTipData()
	blocks, err := nodeGRPC.GetBlockByHeight([]uint64{tipData.Metadata.BestBlockHeight})
	if err != nil {
		milieu.CaptureException(err)
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	block := blocks[0]
	netDiff, err := nodeGRPC.GetNetworkDiff(tipData.Metadata.BestBlockHeight)
	if err != nil {
		milieu.CaptureException(err)
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	blockHeader, err := nodeGRPC.GetHeaderByHash(tipData.Metadata.BestBlockHash)
	if err != nil {
		milieu.CaptureException(err)
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	returnData := getLastBlockHeaderResultWrapper{
		ID:      "0",
		Jsonrpc: "2.0",
		Result: getLastBlockHeaderResult{
			BlockHeader: BlockHeader{
				BlockSize:                 0,
				BlockWeight:               0,
				CumulativeDifficulty:      0,
				CumulativeDifficultyTop64: 0,
				Depth:                     0,
				Difficulty:                int64(netDiff.Difficulty),
				DifficultyTop64:           0,
				Hash:                      fmt.Sprintf("%x", block.Header.Hash),
				Height:                    int(tipData.Metadata.BestBlockHeight),
				LongTermWeight:            0,
				MajorVersion:              0,
				MinerTxHash:               "",
				MinorVersion:              0,
				Nonce:                     0,
				NumTxes:                   int(blockHeader.NumTransactions),
				OrphanStatus:              false,
				PowHash:                   "",
				PrevHash:                  fmt.Sprintf("%x", block.Header.PrevHash),
				Reward:                    int64(blockHeader.Reward),
				Timestamp:                 int(block.Header.Timestamp),
				WideCumulativeDifficulty:  "",
				WideDifficulty:            "",
			},
			Credits:   0,
			Status:    "",
			TopHash:   "",
			Untrusted: false,
		},
	}
	c.JSON(200, returnData)
}

func handleGetBlockHeaderByHash(c *gin.Context, bodyAsByteArray []byte) {
	milieu := middleware.MustGetMilieu(c)
	getBlockHeader := getBlockHeaderByHashStuct{}
	if err := json.Unmarshal(bodyAsByteArray, &getBlockHeader); err != nil {
		milieu.CaptureException(err)
		c.Status(400)
		return
	}
	blockHash, err := hex.DecodeString(getBlockHeader.Params.Hash)
	if err != nil {
		c.Status(400)
		return
	}
	blockHeader, err := nodeGRPC.GetHeaderByHash(blockHash)
	if err != nil {
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	netDiff, err := nodeGRPC.GetNetworkDiff(blockHeader.Header.Height)
	if err != nil {
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	returnData := getLastBlockHeaderResultWrapper{
		ID:      "0",
		Jsonrpc: "2.0",
		Result: getLastBlockHeaderResult{
			BlockHeader: BlockHeader{
				BlockSize:                 0,
				BlockWeight:               0,
				CumulativeDifficulty:      0,
				CumulativeDifficultyTop64: 0,
				Depth:                     0,
				Difficulty:                int64(netDiff.Difficulty),
				DifficultyTop64:           0,
				Hash:                      fmt.Sprintf("%x", blockHeader.Header.Hash),
				Height:                    int(blockHeader.Header.Height),
				LongTermWeight:            0,
				MajorVersion:              0,
				MinerTxHash:               "",
				MinorVersion:              0,
				Nonce:                     0,
				NumTxes:                   int(blockHeader.NumTransactions),
				OrphanStatus:              false,
				PowHash:                   "",
				PrevHash:                  fmt.Sprintf("%x", blockHeader.Header.PrevHash),
				Reward:                    int64(blockHeader.Reward),
				Timestamp:                 int(blockHeader.Header.Timestamp),
				WideCumulativeDifficulty:  "",
				WideDifficulty:            "",
			},
			Credits:   0,
			Status:    "",
			TopHash:   "",
			Untrusted: false,
		},
	}
	c.JSON(200, returnData)
}

func handleSubmitBlock(c *gin.Context, bodyAsByteArray []byte) {
	milieu := middleware.MustGetMilieu(c)
	// This is a submit block request
	submitBlock := submitBlockStruct{}

	err := json.Unmarshal(bodyAsByteArray, &submitBlock)
	if err != nil {
		return
	}
	// Load the MM hash from the BT, bytes 3:35
	rawTariBt, err := hex.DecodeString(submitBlock.Params[0])
	if err != nil {
		milieu.CaptureException(err)
		c.JSON(400, rpcResultError{
			Jsonrpc: "2.0",
			ID:      "-1",
			Error:   err.Error(),
		})
		return
	}
	mmHash := rawTariBt[3:35]
	// Read lock time
	tariBlockCacheLock.RLock()
	defer tariBlockCacheLock.RUnlock()
	if v, ok := tariBlockCache[fmt.Sprintf("%x", mmHash)]; ok {
		blockData := v.Block
		blockData.Header.Nonce = uint64(binary.BigEndian.Uint32(rawTariBt[39:43]))
		blockData.Header.Pow.PowData = rawTariBt[44:76]
		if blockResp, err := nodeGRPC.SubmitBlock(blockData); err != nil {
			milieu.CaptureException(err)
			c.JSON(400, rpcResultError{
				Jsonrpc: "2.0",
				ID:      "-1",
				Error:   err.Error(),
			})
			return
		} else {
			c.JSON(200, gin.H{"result": fmt.Sprintf("%x", blockResp.BlockHash)})
			for _, grpcNode := range grpcNodeList {
				go func() {
					var opts []grpc.DialOption
					opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
					conn, _ := grpc.NewClient(grpcNode, opts...)
					defer conn.Close()
					client := tari_generated.NewBaseNodeClient(conn)
					client.SubmitBlock(context.Background(), blockData)
				}()
			}
		}
	} else {
		c.JSON(400, rpcResultError{
			Jsonrpc: "2.0",
			ID:      "-1",
			Error:   "Merge mining tag not found in cache.",
		})
		return
	}

}

func handleGetBlockTemplate(c *gin.Context, bodyAsByteArray []byte) {
	milieu := middleware.MustGetMilieu(c)
	getBlockTemplate := getBlockTemplateStruct{}
	if err := json.Unmarshal(bodyAsByteArray, &getBlockTemplate); err != nil {
		milieu.CaptureException(err)
		c.Status(400)
		return
	}
	tariBlockCacheLock.RLock()
	defer tariBlockCacheLock.RUnlock()
	// 0 - Major
	// 1 - Minor
	// 2 - Null
	// 3-34 - MM Hash
	// 35-38 - Null timestamp bytes
	// 39-42 - Nonce high bytes
	// 43-46 - Nonce low bytes, where you'll find them.
	// Major, Minor, Null
	tariJsonRPCBt := []byte{0x00, 0x00, 0x00}
	// Tari MM Hash
	tariJsonRPCBt = append(tariJsonRPCBt, tariTipBlock.MergeMiningHash...)
	// 4 null bytes, this is the 4 bytes that normally would be part of timestamp, which is where null at 3 comes from
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00}...)
	// 4 null bytes, this is the nonce space, then 0x02, a magic byte
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x02}...)
	// 32 null bytes, this is the PoWData slab, which we'll expose as reserve_offset
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)

	returnStruct := daemon.BlockTemplateResponse{
		ID:      getBlockTemplate.ID,
		Jsonrpc: getBlockTemplate.Jsonrpc,
		Result: struct {
			BlockhashingBlob  string `json:"blockhashing_blob"`
			BlocktemplateBlob string `json:"blocktemplate_blob"`
			Difficulty        int64  `json:"difficulty"`
			DifficultyTop64   int    `json:"difficulty_top64"`
			ExpectedReward    int64  `json:"expected_reward"`
			Height            int    `json:"height"`
			NextSeedHash      string `json:"next_seed_hash"`
			PrevHash          string `json:"prev_hash"`
			ReservedOffset    int    `json:"reserved_offset"`
			SeedHash          string `json:"seed_hash"`
			SeedHeight        int    `json:"seed_height"`
			Status            string `json:"status"`
			Untrusted         bool   `json:"untrusted"`
			WideDifficulty    string `json:"wide_difficulty"`
		}{},
	}

	returnStruct.Result.BlockhashingBlob = fmt.Sprintf("%x", []byte{0x00})
	returnStruct.Result.BlocktemplateBlob = fmt.Sprintf("%x", tariJsonRPCBt)
	returnStruct.Result.Difficulty = int64(tariTipBlock.MinerData.TargetDifficulty)
	returnStruct.Result.DifficultyTop64 = 0
	returnStruct.Result.ExpectedReward = int64(tariTipBlock.MinerData.Reward + tariTipBlock.MinerData.TotalFees)
	returnStruct.Result.Height = int(tariTipBlock.Block.Header.Height)
	returnStruct.Result.NextSeedHash = fmt.Sprintf("%x", tariJsonRPCBt)
	returnStruct.Result.PrevHash = fmt.Sprintf("%x", tariTipBlock.MergeMiningHash)
	returnStruct.Result.ReservedOffset = 47
	returnStruct.Result.SeedHash = fmt.Sprintf("%x", tariTipBlock.VmKey)
	returnStruct.Result.SeedHeight = int(tariTipBlock.Block.Header.Height)
	returnStruct.Result.Status = fmt.Sprintf("%x", tariTipBlock.BlockHash)
	returnStruct.Result.Untrusted = false
	returnStruct.Result.WideDifficulty = fmt.Sprintf("0x%x", tariTipBlock.MinerData.TargetDifficulty)
	returnStruct.ID = getBlockTemplate.ID
	returnStruct.Jsonrpc = getBlockTemplate.Jsonrpc
	c.JSON(200, returnStruct)
}

func updateTariBlockCache(milieu *core.Milieu) {
	blockTemplateCache.UpdateBlockTemplateCache(milieu)
	blockData, err := blockTemplateCache.GetBlockRandomX(poolMinerID, tariPoolPayoutAddress)
	if err != nil {
		milieu.CaptureException(err)
		milieu.Info(err.Error())
		return
	}
	// Lock the cache, and begin.
	tariBlockCacheLock.Lock()
	defer tariBlockCacheLock.Unlock()
	if tariTipBlock != nil {
		if tariTipBlock.Block.Header.Height == blockData.Block.Header.Height {
			// We're not interested if the tip block hasn't changed.
			return
		}
	}
	tariTipBlock = blockData
	tariBlockCache[fmt.Sprintf("%x", blockData.MergeMiningHash)] = blockData
	tariBlockCacheList = append(tariBlockCacheList, fmt.Sprintf("%x", blockData.MergeMiningHash))
}

func main() {
	// Build Milieu
	sentry := helpers.GetEnv("SENTRY_SERVER", "")
	milieu, err := core.NewMilieu(nil, nil, &sentry)
	if err != nil {
		milieu.CaptureException(err)
		milieu.Fatal(err.Error())
	}
	// Milieu initialized

	// Load config flags
	debugEnabledPtr := flag.Bool("debug-enabled", false, "Enable Debug Logging")
	soloEnabledPtr := flag.Bool("solo", false, "Enable solo mode")
	nodeGRPCPtr := flag.String("base-node-grpc-address", "node-pool.tari.jagtech.io:18102", "Address for the base-node, defaults to Impala's public pool")
	poolStringIDPtr := flag.String("pool-coinbase-id", "ImpalaDev", "9 character string to identify the pool")
	tariPoolAddress := flag.String("pool-tari-address", "1215dapiKwqGxk9TAjELMf9gnH6iKM5B9gLbMBvtDSVATRtnBsKDN8bfxGECaPC1wwA8AwRLnq1Ycg28Qx71uW8pABi", "The address of the wallet Tari should be paid into, the shim ignores the data in GBT")
	minDiff := flag.Uint64("min-diff", 100000, "Set the minimum difficulty for the port")
	maxDiff := flag.Uint64("max-diff", 5000000, "Set the maximum difficulty for the port")
	startingDiff := flag.Uint64("starting-diff", 100000, "Set the starting difficulty for the port")
	flag.Parse()

	poolID := []byte(*poolStringIDPtr)
	blockTemplateCache.PoolStringID = &poolID
	tariPoolPayoutAddress = *tariPoolAddress
	mainGRPCNode = *nodeGRPCPtr
	isSoloMode = *soloEnabledPtr
	config.StartingDifficulty = *startingDiff
	config.MinimumDifficulty = *minDiff
	config.MaxDifficulty = *maxDiff
	nodeGRPC.InitNodeGRPC(*nodeGRPCPtr)
	config.InitShareCount()

	// Initalize the caches
	tipDataCache.UpdateTipData(milieu)
	updateTariBlockCache(milieu)

	if *debugEnabledPtr {
		milieu.SetLogLevel(logrus.DebugLevel)
	}

	// Repeating task setup start
	crons := cron.New(cron.WithSeconds())

	config.SystemCrons.CronMaster = crons

	// Add tasks to cron
	_, _ = crons.AddFunc("* * * * * *", func() {
		tipDataCache.UpdateTipData(milieu)
	})

	_, _ = crons.AddFunc("* * * * * *", func() {
		updateTariBlockCache(milieu)
	})

	// Add template cleanup job
	_, _ = crons.AddFunc("0 * * * * *", func() {
		// Lock the cache, and begin.
		tariBlockCacheLock.Lock()
		defer tariBlockCacheLock.Unlock()
		newCacheList := make([]string, len(tariBlockCacheList))
		for _, v := range tariBlockCacheList {
			if _, ok := tariBlockCache[v]; ok {
				if tariBlockCache[v].Block.Header.Timestamp < uint64(time.Now().Add(time.Minute*-1*30).Unix()) {
					tariBlockCache[v] = nil
				} else {
					newCacheList = append(newCacheList, fmt.Sprintf("%x", tariBlockCache[v].MergeMiningHash))
				}
			}
		}
		tariBlockCacheList = newCacheList
	})

	_, _ = crons.AddFunc("*/30 * * * * *", func() {
		diff := config.MinerDiff.Get()
		diffText := "0 H/s"
		if diff != 0 {
			diff = diff / 30
		}
		if diff < 1000000000000000 {
			diffText = fmt.Sprintf("%.3f TH/s", float64(diff)/1000000000000)
		}
		if diff < 1000000000000 {
			diffText = fmt.Sprintf("%.3f GH/s", float64(diff)/1000000000)
		}
		if diff < 1000000000 {
			diffText = fmt.Sprintf("%.3f MH/s", float64(diff)/1000000)
		}
		if diff < 1000000 {
			diffText = fmt.Sprintf("%.3f KH/s", float64(diff)/1000)
		}
		if diff < 1000 {
			diffText = fmt.Sprintf("%d H/s", diff)
		}
		milieu.Info(fmt.Sprintf("%v/%v/%v/%v Valid/Trusted/Invalid/Total shares in last 30s - %v",
			config.ShareCount.Get("valid"),
			config.ShareCount.Get("trusted"),
			config.ShareCount.Get("invalid"),
			config.ShareCount.Get("total"),
			diffText))

	})

	crons.Start()

	r := gin.Default()
	r.Use(middleware.SetupMilieu(milieu))
	r.POST("/json_rpc", func(c *gin.Context) {
		// Lets begin, shall we?  Play a game perhaps?
		// We're handling 2 JSON-RPC calls, we handle get_block_template, and
		bodyAsByteArray, _ := io.ReadAll(c.Request.Body)
		jsonBody := string(bodyAsByteArray)
		if strings.Contains(jsonBody, "submitblock") {
			handleSubmitBlock(c, bodyAsByteArray)
		} else if strings.Contains(jsonBody, "getblocktemplate") {
			handleGetBlockTemplate(c, bodyAsByteArray)
		} else if strings.Contains(jsonBody, "getblockheaderbyhash") {
			handleGetBlockHeaderByHash(c, bodyAsByteArray)
		} else if strings.Contains(jsonBody, "getlastblockheader") {
			handleGetLastBlockHeader(c)
		} else {
			milieu.CaptureException(fmt.Errorf("Unsupported json request"))
			c.Status(400)
			return
		}
	})

	go func() {
		listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%v", 2738))
		fmt.Printf("Listening on port 2738 for stratum connections\n")
		if err != nil {
			milieu.CaptureException(err)
			milieu.Fatal(err.Error())
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				break
			}
			defer conn.Close()
			go poolStratum.ClientConn(milieu)(conn)
		}
	}()

	err = r.Run("127.0.0.1:1330") // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
	if err != nil {
		milieu.CaptureException(err)
		log.Fatalf("Unable to initalize gin")
	}

}
