// macOS 系统音频捕获，基于 ScreenCaptureKit。
//
// CoreAudio 不提供回环，所以录"系统正在播的声音"在 macOS 上只有两条路：让用户装
// BlackHole 这类虚拟声卡，或者用 ScreenCaptureKit。选后者——前者要用户自己装一个
// 内核扩展级别的东西、还要去声音设置里改输出设备，出了问题也无从排查。
//
// ScreenCaptureKit 本是屏幕录制接口，capturesAudio 让它同时给出系统音频。代价是
// 必须同时"捕获"点画面（下面把它压到 2×2、1 fps），以及需要一次「屏幕录制」授权
// ——首次启动时系统会弹框，用户拒绝的话这里以明确的错误返回，录音入口据此置灰。

#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <AudioToolbox/AudioToolbox.h>

#include "sysaudio_darwin.h"

API_AVAILABLE(macos(13.0))
@interface RCSysAudio : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic, strong) SCStream *stream;
@property(nonatomic, assign) uintptr_t handle;
@property(nonatomic, assign) rc_sysaudio_cb cb;
@property(nonatomic, assign) int channels;
// interleaved 是把 ScreenCaptureKit 的分平面数据交错后的缓冲，跨回调复用：
// 回调以约 100 Hz 触发，每次都分配是实时路径抖动的经典来源。
@property(nonatomic, assign) float *interleaved;
@property(nonatomic, assign) int interleavedCap;
@end

@implementation RCSysAudio

- (void)dealloc {
  if (_interleaved) {
    free(_interleaved);
    _interleaved = NULL;
  }
}

// SCStreamDelegate：流自己停了（比如显示器配置变了）。这里不做重连——录音会话
// 的生命周期归 Go 侧管，静默重连会让"录到一半断了"变得无法察觉。
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
  (void)stream;
  (void)error;
}

- (void)stream:(SCStream *)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
                   ofType:(SCStreamOutputType)type {
  (void)stream;
  if (type != SCStreamOutputTypeAudio || self.cb == NULL) {
    return;
  }
  if (!CMSampleBufferDataIsReady(sampleBuffer)) {
    return;
  }

  // ScreenCaptureKit 给的是**分平面**（non-interleaved）float32：每个声道一个
  // buffer。而 DSP 链那边（与 malgo 共用）吃的是交错格式，所以这里要转一次。
  AudioBufferList abl;
  CMBlockBufferRef block = NULL;
  OSStatus st = CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer(
      sampleBuffer, NULL, &abl, sizeof(abl), NULL, NULL,
      kCMSampleBufferFlag_AudioBufferList_Assure16ByteAlignment, &block);
  if (st != noErr || abl.mNumberBuffers == 0) {
    if (block) CFRelease(block);
    return;
  }

  int planes = (int)abl.mNumberBuffers;
  int frames = (int)(abl.mBuffers[0].mDataByteSize / sizeof(float));
  if (frames <= 0) {
    CFRelease(block);
    return;
  }

  int need = frames * planes;
  if (self.interleavedCap < need) {
    free(self.interleaved);
    self.interleaved = (float *)malloc((size_t)need * sizeof(float));
    self.interleavedCap = self.interleaved ? need : 0;
  }
  if (self.interleaved == NULL) {
    CFRelease(block);
    return;
  }

  float *out = self.interleaved;
  for (int p = 0; p < planes; p++) {
    const float *src = (const float *)abl.mBuffers[p].mData;
    if (src == NULL) {
      CFRelease(block);
      return;
    }
    for (int i = 0; i < frames; i++) {
      out[i * planes + p] = src[i];
    }
  }

  self.cb(self.handle, out, frames, planes);
  CFRelease(block);
}

@end

int rc_sysaudio_available(void) {
  if (@available(macOS 13.0, *)) {
    return 1;
  }
  return 0;
}

int rc_sysaudio_start(uintptr_t handle, rc_sysaudio_cb cb, int sample_rate, int channels,
                      void **out, char *errbuf, int errlen) {
  if (out == NULL) {
    return 1;
  }
  *out = NULL;

  if (@available(macOS 13.0, *)) {
    __block NSError *failure = nil;
    __block SCShareableContent *content = nil;
    // getShareableContent 是异步的，而这一层对 Go 承诺同步返回（连同用户授权那步）。
    // 用信号量等它，超时后按失败处理——授权弹框没人点的话它不会自己回来。
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [SCShareableContent
        getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
          content = c;
          failure = e;
          dispatch_semaphore_signal(sem);
        }];
    if (dispatch_semaphore_wait(
            sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(60 * NSEC_PER_SEC))) != 0) {
      snprintf(errbuf, (size_t)errlen, "等待屏幕录制授权超时");
      return 1;
    }
    if (failure != nil || content == nil || content.displays.count == 0) {
      const char *msg = failure ? failure.localizedDescription.UTF8String : "没有可用的显示器";
      // 用户没给「屏幕录制」权限时走的就是这条。
      snprintf(errbuf, (size_t)errlen, "无法访问屏幕录制：%s", msg ? msg : "未知错误");
      return 1;
    }

    SCDisplay *display = content.displays.firstObject;
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display
                                                     excludingWindows:@[]];

    SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
    cfg.capturesAudio = YES;
    cfg.sampleRate = sample_rate;
    cfg.channelCount = channels;
    // 不录自己发出的声音：否则回放提示音之类会被录进去，更糟的是会形成回授。
    cfg.excludesCurrentProcessAudio = YES;
    // 视频这一路要不起，但 SCStream 必须有个捕获目标。压到最小尺寸与最低帧率，
    // 让它的开销可以忽略——设 0 会被拒绝。
    cfg.width = 2;
    cfg.height = 2;
    cfg.minimumFrameInterval = CMTimeMake(1, 1);
    cfg.showsCursor = NO;

    RCSysAudio *self_ = [[RCSysAudio alloc] init];
    self_.handle = handle;
    self_.cb = cb;
    self_.channels = channels;

    SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:cfg delegate:self_];
    self_.stream = stream;

    NSError *addErr = nil;
    dispatch_queue_t q = dispatch_queue_create("cn.ouconline.ai.sysaudio", DISPATCH_QUEUE_SERIAL);
    if (![stream addStreamOutput:self_
                            type:SCStreamOutputTypeAudio
              sampleHandlerQueue:q
                           error:&addErr]) {
      const char *msg = addErr ? addErr.localizedDescription.UTF8String : "未知错误";
      snprintf(errbuf, (size_t)errlen, "挂接音频输出失败：%s", msg ? msg : "未知错误");
      return 1;
    }

    __block NSError *startErr = nil;
    dispatch_semaphore_t started = dispatch_semaphore_create(0);
    [stream startCaptureWithCompletionHandler:^(NSError *e) {
      startErr = e;
      dispatch_semaphore_signal(started);
    }];
    if (dispatch_semaphore_wait(
            started, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(30 * NSEC_PER_SEC))) != 0) {
      snprintf(errbuf, (size_t)errlen, "启动系统音频捕获超时");
      return 1;
    }
    if (startErr != nil) {
      const char *msg = startErr.localizedDescription.UTF8String;
      snprintf(errbuf, (size_t)errlen, "启动系统音频捕获失败：%s", msg ? msg : "未知错误");
      return 1;
    }

    // 交给 Go 持有：CFBridgingRetain 把 ARC 的所有权转出去，rc_sysaudio_stop 里
    // 用 CFBridgingRelease 收回。不这么做的话对象在本函数返回时就被释放，
    // 表现是回调再也不来，而且没有任何报错。
    *out = (void *)CFBridgingRetain(self_);
    return 0;
  }

  snprintf(errbuf, (size_t)errlen, "录系统声音需要 macOS 13 或更高版本");
  return 1;
}

void rc_sysaudio_stop(void *capture) {
  if (capture == NULL) {
    return;
  }
  if (@available(macOS 13.0, *)) {
    RCSysAudio *self_ = (RCSysAudio *)CFBridgingRelease(capture);
    // 先断回调再停流：stopCapture 是异步的，期间可能还有一两次回调在路上，
    // 而那时 Go 侧的 handle 可能已经失效了。
    self_.cb = NULL;
    [self_.stream stopCaptureWithCompletionHandler:^(NSError *e) {
      (void)e;
    }];
    self_.stream = nil;
  }
}
