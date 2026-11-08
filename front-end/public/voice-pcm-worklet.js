// 麦克风 PCM 采集：把 128 样本的 render quantum 攒成约 2048 样本再发给主线程，
// 避免每秒 375 次 postMessage。重采样到 16k 在主线程做（AudioContext 采样率因设备而异）。
class VoicePCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super()
    this.buffer = new Float32Array(2048)
    this.length = 0
  }

  process(inputs) {
    const channel = inputs[0] && inputs[0][0]
    if (!channel) return true
    let offset = 0
    while (offset < channel.length) {
      const space = this.buffer.length - this.length
      const take = Math.min(space, channel.length - offset)
      this.buffer.set(channel.subarray(offset, offset + take), this.length)
      this.length += take
      offset += take
      if (this.length === this.buffer.length) {
        const out = this.buffer.slice(0)
        this.port.postMessage(out, [out.buffer])
        this.length = 0
      }
    }
    return true
  }
}

registerProcessor('voice-pcm', VoicePCMProcessor)
