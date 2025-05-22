package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Snipa22/core-go-lib/helpers"
	core "github.com/Snipa22/core-go-lib/milieu"
	"github.com/Snipa22/core-go-lib/milieu/middleware"
	"github.com/Snipa22/go-tari-grpc-lib/v2/nodeGRPC"
	"github.com/Snipa22/go-tari-grpc-lib/v2/tari_generated"
	"github.com/Snipa22/go-xmr-lib/daemon"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/snipa22/go-tari-pool-shim/subsystems/blockTemplateCache"
	"github.com/snipa22/go-tari-pool-shim/subsystems/tipDataCache"
	"io"
	"log"
	"strings"
	"sync"
	"time"
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
var tariBlockCache = map[string]*tari_generated.GetNewBlockResult{}
var tariBlockCacheList []string
var tariBlockCacheLock sync.RWMutex
var tariPoolPayoutAddress = "1215dapiKwqGxk9TAjELMf9gnH6iKM5B9gLbMBvtDSVATRtnBsKDN8bfxGECaPC1wwA8AwRLnq1Ycg28Qx71uW8pABi"
var poolMinerID []byte

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
	PrevHash                  string `json:"previous_hash"`
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
		milieu.CaptureException(err)
		c.Status(400)
		return
	}
	blockHeader, err := nodeGRPC.GetHeaderByHash(blockHash)
	if err != nil {
		milieu.CaptureException(err)
		milieu.Info(err.Error())
		c.Status(500)
		return
	}
	netDiff, err := nodeGRPC.GetNetworkDiff(blockHeader.Header.Height)
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
	json.Unmarshal(bodyAsByteArray, &submitBlock)
	// Load the MM hash from the BT, bytes 3:35
	rawTariBt, err := hex.DecodeString(submitBlock.Params[0])
	if err != nil {
		milieu.CaptureException(err)
		c.Status(400)
		return
	}
	mmHash := rawTariBt[3:35]
	// Read lock time
	tariBlockCacheLock.RLock()
	defer tariBlockCacheLock.RUnlock()
	if v, ok := tariBlockCache[fmt.Sprintf("%x", mmHash)]; ok {
		blockData := v.Block
		blockData.Header.Nonce = uint64(binary.LittleEndian.Uint32(rawTariBt[43:47]))
		if blockResp, err := nodeGRPC.SubmitBlock(blockData); err != nil {
			milieu.CaptureException(err)
			c.Status(400)
			return
		} else {
			c.JSON(200, gin.H{"result": fmt.Sprintf("%v", blockResp.BlockHash)})
		}
	} else {
		milieu.Info("Merge mining tag not found in cache.")
		c.Status(400)
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
	// 8 null bytes, this is the nonce space
	tariJsonRPCBt = append(tariJsonRPCBt, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...)
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
	returnStruct.Result.Status = "OK"
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
	nodeGRPCPtr := flag.String("base-node-grpc-address", "node-pool.tari.jagtech.io:18102", "Address for the base-node, defaults to Impala's public pool")
	poolStringIDPtr := flag.String("pool-coinbase-id", "ImpalaDev", "9 character string to identify the pool")
	tariPoolAddress := flag.String("pool-tari-address", "1215dapiKwqGxk9TAjELMf9gnH6iKM5B9gLbMBvtDSVATRtnBsKDN8bfxGECaPC1wwA8AwRLnq1Ycg28Qx71uW8pABi", "The address of the wallet Tari should be paid into, the shim ignores the data in GBT")
	flag.Parse()

	poolID := []byte(*poolStringIDPtr)
	blockTemplateCache.PoolStringID = &poolID
	tariPoolPayoutAddress = *tariPoolAddress

	nodeGRPC.InitNodeGRPC(*nodeGRPCPtr)

	// Initalize the caches
	tipDataCache.UpdateTipData(milieu)

	if *debugEnabledPtr {
		milieu.SetLogLevel(logrus.DebugLevel)
	}

	// Repeating task setup start
	crons := cron.New(cron.WithSeconds())

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
		} else if strings.Contains(jsonBody, "getlastblockheaderbyhash") {
			handleGetBlockHeaderByHash(c, bodyAsByteArray)
		} else if strings.Contains(jsonBody, "getlastblockheader") {
			handleGetLastBlockHeader(c)
		} else {
			milieu.CaptureException(fmt.Errorf("Unsupported json request"))
			c.Status(400)
			return
		}
	})

	err = r.Run("127.0.0.1:1330") // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
	if err != nil {
		milieu.CaptureException(err)
		log.Fatalf("Unable to initalize gin")
	}
}
