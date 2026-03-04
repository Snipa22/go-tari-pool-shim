package blockTemplateCache

import (
	"encoding/binary"
	"math/rand"
	"sync"

	"github.com/Snipa22/core-go-lib/milieu"
	"github.com/Snipa22/go-tari-grpc-lib/v3/nodeGRPC"
	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
)

// poolID is a random byte string used to ID the pool in the coinbase txn
var poolID *[]byte

// PoolStringID is a 9 character string used to ID the pool in the coinbase txn, this is a list of valid ones.
var PoolStringID *[]byte

// Yes, I'm writing a function to turn strings into []bytes

// blockTemplateCacheStruct contains the response from a GetBlockTemplate, this is used to generate the
// GetNewBlockTemplateWithCoinbasesRequest that is sent to the Tari daemon, this is needed to build the base coinbase
// transaction, which needs to contain the pool ID and other data related to the miner unique nonce.
type blockTemplateCacheStruct struct {
	blockTemplate *tari_generated.NewBlockTemplate
	reward        uint64
	mutex         sync.RWMutex
}

var sha3xBTCache *blockTemplateCacheStruct = nil
var rxBTCache *blockTemplateCacheStruct = nil
var running = false

// When a miner requests a block template, we need to update the coinbase txn with their unique hash and the pool hash

// UpdateBlockTemplateCache keeps the main block template system spinning and updating to keep things clean
func UpdateBlockTemplateCache(core *milieu.Milieu) {
	if running {
		return
	}
	running = true
	defer func() {
		running = false
	}()
	if poolID == nil {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, rand.Uint64())
		poolID = &buf
	}

	blockTemplateResponse, err := nodeGRPC.GetBlockTemplate(&tari_generated.PowAlgo{PowAlgo: tari_generated.PowAlgo_POW_ALGOS_SHA3X})
	if err != nil {
		core.CaptureException(err)
		return
	}
	if sha3xBTCache == nil {
		sha3xBTCache = &blockTemplateCacheStruct{}
	}

	sha3xBTCache.mutex.Lock()
	sha3xBTCache.blockTemplate = blockTemplateResponse.GetNewBlockTemplate()
	sha3xBTCache.reward = blockTemplateResponse.MinerData.Reward
	sha3xBTCache.mutex.Unlock()

	blockTemplateResponse, err = nodeGRPC.GetBlockTemplate(&tari_generated.PowAlgo{PowAlgo: tari_generated.PowAlgo_POW_ALGOS_RANDOMXT})
	if err != nil {
		core.CaptureException(err)
		return
	}
	if rxBTCache == nil {
		rxBTCache = &blockTemplateCacheStruct{}
	}
	rxBTCache.mutex.Lock()
	rxBTCache.blockTemplate = blockTemplateResponse.GetNewBlockTemplate()
	rxBTCache.reward = blockTemplateResponse.MinerData.Reward
	rxBTCache.mutex.Unlock()
}

// GetBlockRandomX uses the block template cache to call GetBlockWithCoinbases with the miner ID as well as the
// pool ID in the coinbase TXN, this is essentially a hard-wrapper for the GetNewBlock GRPC call.
func GetBlockRandomX(minerID []byte, poolAddress string) (*tari_generated.GetNewBlockResult, error) {

	// Generate the coinbase Extra
	coinbaseExtra := make([]byte, 40)
	/*
		Format is as follows:
		0-2: "WUF"
		3-12: Squad Identifier - poolStringIDList
		13-20: PoolID
		21-28: MinerID
		29-37: RandomData
		37-39: "WUF"
	*/
	coinbaseExtra[0] = 0x57
	coinbaseExtra[1] = 0x55
	coinbaseExtra[2] = 0x46
	coinbaseExtra[37] = 0x57
	coinbaseExtra[38] = 0x55
	coinbaseExtra[39] = 0x46
	for i, v := range *PoolStringID {
		coinbaseExtra[i+3] = v
	}
	for i, v := range *poolID {
		coinbaseExtra[i+13] = v
	}
	for i, v := range minerID {
		coinbaseExtra[i+21] = v
	}
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, rand.Uint64())
	for i, v := range buf {
		coinbaseExtra[i+28] = v
	}

	// Get the blockTemplate from the cache
	rxBTCache.mutex.RLock()
	localBT := rxBTCache.blockTemplate
	blockValue := rxBTCache.reward
	rxBTCache.mutex.RUnlock()

	for _, v := range localBT.Body.Kernels {
		blockValue += v.Fee
	}

	// Generate the coinbase transactions
	coinbaseData := make([]*tari_generated.NewBlockCoinbase, 0)
	i := 0
	perCoinbase := blockValue / 10
	for {
		if blockValue < perCoinbase {
			break
		}
		binary.LittleEndian.PutUint64(buf, rand.Uint64())
		for i, v := range buf {
			coinbaseExtra[i+28] = v
		}
		coinbaseData = append(coinbaseData, &tari_generated.NewBlockCoinbase{
			Address:            poolAddress,
			Value:              perCoinbase,
			StealthPayment:     false,
			RevealedValueProof: true,
			CoinbaseExtra:      coinbaseExtra,
		})
		blockValue -= perCoinbase
		i++
		if i > 5 {
			break
		}
	}
	perCoinbase = blockValue / 100
	for {
		if blockValue < perCoinbase {
			break
		}
		binary.LittleEndian.PutUint64(buf, rand.Uint64())
		for i, v := range buf {
			coinbaseExtra[i+28] = v
		}
		coinbaseData = append(coinbaseData, &tari_generated.NewBlockCoinbase{
			Address:            poolAddress,
			Value:              perCoinbase,
			StealthPayment:     false,
			RevealedValueProof: true,
			CoinbaseExtra:      coinbaseExtra,
		})
		blockValue -= perCoinbase
		i++
		if i > 10 {
			break
		}
	}
	if blockValue > 0 {
		binary.LittleEndian.PutUint64(buf, rand.Uint64())
		for i, v := range buf {
			coinbaseExtra[i+28] = v
		}
		coinbaseData = append(coinbaseData, &tari_generated.NewBlockCoinbase{
			Address:            poolAddress,
			Value:              blockValue,
			StealthPayment:     false,
			RevealedValueProof: true,
			CoinbaseExtra:      coinbaseExtra,
		})
	}

	// Get the block data w/ the coinbases
	return nodeGRPC.GetBlockWithCoinbases(&tari_generated.GetNewBlockWithCoinbasesRequest{
		NewTemplate: localBT,
		Coinbases:   coinbaseData,
	})
}
