package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GPUManager struct {
	nvidiaSmi  bool
	rocmSmi    bool
	GpuDataArr []GPUData
	mutex      sync.Mutex
}

type rocmSmiInfo struct {
	ID          string `json:"Device ID"`
	Name        string `json:"Card series"`
	Temperature string `json:"Temperature (Sensor edge) (C)"`
	MemoryUsed  string `json:"VRAM Total Used Memory (B)"`
	MemoryTotal string `json:"VRAM Total Memory (B)"`
	Usage       string `json:"GPU use (%)"`
	Power       string `json:"Current Socket Graphics Package Power (W)"`
}

type GPUData struct {
	ID          string  `json:"-"`
	Name        string  `json:"n"`
	Temperature float64 `json:"-"`
	MemoryUsed  float64 `json:"mu,omitempty"`
	MemoryTotal float64 `json:"mt,omitempty"`
	Usage       float64 `json:"u"`
	Power       float64 `json:"p,omitempty"`
	count       float64 `json:"-"`
}

func (gm *GPUManager) parseNvidiaData(output []byte) {
	gm.mutex.Lock()
	defer gm.mutex.Unlock()
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line != "" {
			fields := strings.Split(line, ", ")
			if len(fields) >= 6 {
				id := fields[0]
				temp, _ := strconv.ParseFloat(fields[2], 64)
				memoryUsage, _ := strconv.ParseFloat(fields[3], 64)
				totalMemory, _ := strconv.ParseFloat(fields[4], 64)
				usage, _ := strconv.ParseFloat(fields[5], 64)
				power, _ := strconv.ParseFloat(fields[6], 64)
				var exists bool
				for i, gpu := range gm.GpuDataArr {
					if gpu.ID == id {
						exists = true
						gm.GpuDataArr[i].Temperature += temp
						gm.GpuDataArr[i].MemoryUsed += memoryUsage
						gm.GpuDataArr[i].MemoryTotal += totalMemory
						gm.GpuDataArr[i].Usage += usage
						gm.GpuDataArr[i].Power += power
						gm.GpuDataArr[i].count++
						break
					}
				}
				if !exists {
					gm.GpuDataArr = append(gm.GpuDataArr, GPUData{
						ID:          id,
						Name:        fields[1],
						Temperature: temp,
						MemoryUsed:  memoryUsage,
						MemoryTotal: totalMemory,
						Usage:       usage,
						Power:       power,
						count:       1,
					})
				}
			}
		}
	}
	// fmt.Println(gm.GpuDataArr)
}

func (gm *GPUManager) startNvidiaCollector() error {
	// Set up the command
	cmd := exec.Command("nvidia-smi", "-l", "1", "--query-gpu=index,name,temperature.gpu,memory.used,memory.total,utilization.gpu,power.draw", "--format=csv,noheader,nounits")
	// Set up a pipe to capture stdout
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("Error creating StdoutPipe:", err)
		return err
	}
	// Start the command
	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting command:", err)
		return err
	}
	// Use a scanner to read each line of output
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		gm.parseNvidiaData(line) // Run your function on each new line
	}
	// Check for any errors encountered during scanning
	if err := scanner.Err(); err != nil {
		return err
	}
	// Wait for the command to complete
	if err := cmd.Wait(); err != nil {
		return err
	}
	return nil
}

func (gm *GPUManager) parseAmdData(rocmSmiInfo *map[string]rocmSmiInfo) {
	for _, v := range *rocmSmiInfo {
		temp, _ := strconv.ParseFloat(v.Temperature, 64)
		memoryUsage, _ := strconv.ParseFloat(v.MemoryUsed, 64)
		totalMemory, _ := strconv.ParseFloat(v.MemoryTotal, 64)
		usage, _ := strconv.ParseFloat(v.Usage, 64)
		power, _ := strconv.ParseFloat(v.Power, 64)
		memoryUsage = bytesToMegabytes(memoryUsage)
		totalMemory = bytesToMegabytes(totalMemory)

		var exists bool
		for i, gpu := range gm.GpuDataArr {
			if gpu.ID == v.ID {
				exists = true
				gm.GpuDataArr[i].Temperature += temp
				gm.GpuDataArr[i].MemoryUsed += memoryUsage
				gm.GpuDataArr[i].MemoryTotal += totalMemory
				gm.GpuDataArr[i].Usage += usage
				gm.GpuDataArr[i].Power += power
				gm.GpuDataArr[i].count++
				break
			}
		}
		if !exists {
			gm.GpuDataArr = append(gm.GpuDataArr, GPUData{
				ID:          v.ID,
				Name:        v.Name,
				Temperature: temp,
				MemoryUsed:  memoryUsage,
				MemoryTotal: totalMemory,
				Usage:       usage,
				Power:       power,
				count:       1,
			})
		}
		// fmt.Println(data)
	}
}

func (gm *GPUManager) startAmdCollector() error {
	var rocmSmiInfo map[string]rocmSmiInfo
	for {
		cmd := exec.Command("rocm-smi", "--showid", "--showtemp", "--showuse", "--showpower", "--showproductname", "--showmeminfo", "vram", "--json")

		// Create a buffer to capture standard output
		var stdoutBuffer bytes.Buffer
		cmd.Stdout = &stdoutBuffer

		err := cmd.Run()
		if err != nil {
			fmt.Println("Error:", err)
		}

		err = json.Unmarshal(stdoutBuffer.Bytes(), &rocmSmiInfo)
		if err != nil {
			fmt.Println("Error:", err)
		}

		// fmt.Println(rocmSmiInfo)
		gm.parseAmdData(&rocmSmiInfo)

		time.Sleep(time.Second * 1)
	}
}

// sums and resets the current GPU utilization data since the last update
func (gm *GPUManager) GetCurrentData() ([]GPUData, error) {
	gm.mutex.Lock()
	defer gm.mutex.Unlock()
	// copy the data
	gpuData := make([]GPUData, len(gm.GpuDataArr))
	copy(gpuData, gm.GpuDataArr)

	// sum the data
	for i, gpu := range gpuData {
		gpuData[i].Temperature = twoDecimals(gpu.Temperature / gpu.count)
		gpuData[i].MemoryUsed = twoDecimals(gpu.MemoryUsed / gpu.count)
		gpuData[i].MemoryTotal = twoDecimals(gpu.MemoryTotal / gpu.count)
		gpuData[i].Usage = twoDecimals(gpu.Usage / gpu.count)
		gpuData[i].Power = twoDecimals(gpu.Power / gpu.count)
	}

	// reset the data
	gm.GpuDataArr = make([]GPUData, 0, len(gm.GpuDataArr))

	return gpuData, nil
}

// detectGPU returns the GPU brand (nvidia or amd) or an error if none is found
// todo: make sure there's actually a GPU, not just if the command exists
func (gm *GPUManager) detectGPU() error {
	if err := exec.Command("nvidia-smi").Run(); err == nil {
		gm.nvidiaSmi = true
	}
	if err := exec.Command("rocm-smi").Run(); err == nil {
		gm.rocmSmi = true
	}
	if gm.nvidiaSmi || gm.rocmSmi {
		return nil
	}
	return fmt.Errorf("no GPU found - install nvidia-smi or rocm-smi")
}

// NewGPUManager returns a new GPUManager
func NewGPUManager() (*GPUManager, error) {
	gm := GPUManager{
		GpuDataArr: make([]GPUData, 0, 1),
		mutex:      sync.Mutex{},
	}
	err := gm.detectGPU()
	if err != nil {
		return nil, err
	}
	if gm.nvidiaSmi {
		go gm.startNvidiaCollector()
	}
	if gm.rocmSmi {
		go gm.startAmdCollector()
	}
	// go func() {
	for {
		time.Sleep(time.Second * 5)
		data, err := gm.GetCurrentData()
		if err != nil {
			fmt.Println("Error:", err)
		}
		fmt.Println("GPU Data:", data)
	}
	// }()
	return &gm, nil
}
