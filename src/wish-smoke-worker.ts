import {
  ASCII_REFERENCE,
  createAsciiGrid,
  type AsciiGridFrame,
} from "./wish-ascii-grid";

type ShaderProgram = {
  program: WebGLProgram;
  uniforms: Record<string, WebGLUniformLocation | null>;
};

type FloatTextureFormat = {
  name: "r16f" | "rg16f" | "rgba16f";
  internalFormat: number;
  format: number;
  bytesPerPixel: number;
};

type PendingGrid = {
  now: number;
  computeStarted: number;
  sampleStarted: number;
  sampleWidth: number;
  sampleHeight: number;
  issuedAt: number;
  sampleCpuMs?: number;
};

type ReadbackSlot = {
  buffer: WebGLBuffer;
  fence: WebGLSync | null;
};

type RenderTarget = {
  texture: WebGLTexture;
  framebuffer: WebGLFramebuffer;
  width: number;
  height: number;
  textureBytes: number;
};

type DoubleTarget = RenderTarget & {
  read: RenderTarget;
  write: RenderTarget;
  swap: () => void;
};

type WorkerInit = {
  type: "init";
  canvas: OffscreenCanvas;
  viewportWidth: number;
  width: number;
  height: number;
  canvasWidth: number;
  canvasHeight: number;
  pixelRatio: number;
  charWidth: number;
  lineHeight: number;
  gridWidth: number;
  gridHeight: number;
  coverage: [number, number];
  reducedMotion: boolean;
  perfEnabled: boolean;
  mountEpoch: number;
  prewarm?: boolean;
};

type WorkerResize = {
  type: "resize";
  viewportWidth: number;
  width: number;
  height: number;
  canvasWidth: number;
  canvasHeight: number;
  pixelRatio: number;
  charWidth: number;
  lineHeight: number;
  gridWidth: number;
  gridHeight: number;
  coverage: [number, number];
};

type WorkerPointer = {
  type: "pointer";
  x: number;
  y: number;
  active: boolean;
};

type WorkerMessage =
  | WorkerInit
  | WorkerResize
  | {
      type: "activate";
      mountEpoch: number;
      reducedMotion: boolean;
    }
  | WorkerPointer
  | { type: "visibility"; visible: boolean }
  | { type: "motion"; reducedMotion: boolean }
  | { type: "ack"; sequence: number }
  | { type: "request-full" };

type WorkerOutput =
  | {
      type: "ready";
      startupSteps: number;
      renderer?: string;
      vendor?: string;
      unmaskedRenderer?: string;
      unmaskedVendor?: string;
    }
  | {
      type: "frame";
      frame: AsciiGridFrame;
      computeMs: number;
      gridMs: number;
      metrics?: SmokeWorkerMetrics;
    }
  | { type: "activated" }
  | { type: "prepared"; startupSteps: number }
  | { type: "error"; message: string };

type TimingSummary = {
  p50: number;
  p95: number;
  p99: number;
  count: number;
};

type SmokeWorkerMetrics = {
  windowMs: number;
  cpuStep?: TimingSummary;
  stepCount?: TimingSummary;
  sample?: TimingSummary;
  grid?: TimingSummary;
  gpu?: TimingSummary;
  gpuTimer: boolean;
  textureBytes: number;
  canvasWidth: number;
  canvasHeight: number;
  gridWidth: number;
  gridHeight: number;
  gridCells: number;
  pixelRatio: number;
  renderer?: string;
  vendor?: string;
  unmaskedRenderer?: string;
  unmaskedVendor?: string;
  scalarFormat: FloatTextureFormat["name"];
  velocityFormat: FloatTextureFormat["name"];
  readbackMode: "pbo" | "direct";
};

type GpuTimerExtension = {
  TIME_ELAPSED_EXT: number;
  GPU_DISJOINT_EXT: number;
};

const CAMERA_BOTTOM_CUTOFF_ZOOM10 = 0.158724;

class RollingTimings {
  private readonly values: Array<{ value: number; at: number }> = [];
  private readonly maxSamples: number;
  private readonly windowMs: number;

  constructor(maxSamples = 600, windowMs = 10_000) {
    this.maxSamples = maxSamples;
    this.windowMs = windowMs;
  }

  add(value: number, at = performance.now()) {
    if (!Number.isFinite(value)) return;
    this.prune(at);
    this.values.push({ value, at });
    if (this.values.length > this.maxSamples) this.values.shift();
  }

  private prune(now: number) {
    const oldest = now - this.windowMs;
    while (this.values.length && this.values[0].at < oldest)
      this.values.shift();
  }

  summary(now = performance.now()): TimingSummary | undefined {
    this.prune(now);
    if (!this.values.length) return undefined;
    const ordered = this.values
      .map((sample) => sample.value)
      .sort((left, right) => left - right);
    const quantile = (percentile: number) =>
      ordered[
        Math.min(
          ordered.length - 1,
          Math.floor((ordered.length - 1) * percentile),
        )
      ];
    return {
      p50: Math.round(quantile(0.5) * 100) / 100,
      p95: Math.round(quantile(0.95) * 100) / 100,
      p99: Math.round(quantile(0.99) * 100) / 100,
      count: ordered.length,
    };
  }
}

type WorkerScope = {
  onmessage: ((event: MessageEvent<WorkerMessage>) => void) | null;
  postMessage(message: WorkerOutput): void;
  requestAnimationFrame?: (callback: (now: number) => void) => number;
  cancelAnimationFrame?: (handle: number) => void;
};

const scope = self as unknown as WorkerScope;
const requestFrame = (callback: (now: number) => void) => {
  if (scope.requestAnimationFrame)
    return {
      kind: "raf" as const,
      value: scope.requestAnimationFrame(callback),
    };
  return {
    kind: "timeout" as const,
    value: setTimeout(() => callback(performance.now()), 1000 / 60),
  };
};
const cancelFrame = (handle: ReturnType<typeof requestFrame>) => {
  if (handle.kind === "raf") scope.cancelAnimationFrame?.(handle.value);
  else clearTimeout(handle.value);
};

class SmokeRuntime {
  private readonly canvas: OffscreenCanvas;
  private readonly context: WebGL2RenderingContext;
  private readonly perfEnabled: boolean;
  private readonly timingWindowMs = 10_000;
  private readonly cpuStepTimings = new RollingTimings();
  private readonly stepCountTimings = new RollingTimings();
  private readonly sampleTimings = new RollingTimings();
  private readonly gridTimings = new RollingTimings();
  private readonly gpuTimings = new RollingTimings();
  private readonly gpuTimerExtension: GpuTimerExtension | null;
  private readonly pendingGpuQueries: WebGLQuery[] = [];
  private readonly scalarFormat: FloatTextureFormat;
  private readonly velocityFormat: FloatTextureFormat;
  private mountEpoch: number;
  private readonly programs: ShaderProgram[] = [];
  private readonly renderTargets: RenderTarget[] = [];
  private readonly velocity: DoubleTarget;
  private readonly ink: DoubleTarget;
  private readonly pressure: DoubleTarget;
  private readonly divergence: RenderTarget;
  private readonly curl: RenderTarget;
  private advect!: ShaderProgram;
  private advectSmoke!: ShaderProgram;
  private forces!: ShaderProgram;
  private inject!: ShaderProgram;
  private curlProgram!: ShaderProgram;
  private confine!: ShaderProgram;
  private divergenceProgram!: ShaderProgram;
  private pressureProgram!: ShaderProgram;
  private gradient!: ShaderProgram;
  private display!: ShaderProgram;
  private sampleAscii!: ShaderProgram;
  private trimDensity!: ShaderProgram;
  private readonly grid: ReturnType<typeof createAsciiGrid>;
  private reducedMotion: boolean;
  private readonly state = {
    density: 1.23,
    speed: 0.04,
    detail: ASCII_REFERENCE.fontSize,
    zoom: ASCII_REFERENCE.zoom,
    paused: false,
  };
  private simulationWidth: number;
  private simulationHeight: number;
  private densityWidth: number;
  private densityHeight: number;
  private seed: number;
  private viewportWidth: number;
  private width: number;
  private height: number;
  private pixelRatio = 1;
  private coverage: [number, number];
  private textureBytes = 0;
  private lastMetricsPublish = 0;
  private readonly renderer: string;
  private readonly vendor: string;
  private readonly unmaskedRenderer?: string;
  private readonly unmaskedVendor?: string;
  private simulationTime = 0;
  private windTime: number;
  private activated = false;
  private readonly prewarm: boolean;
  private animationFrame: ReturnType<typeof requestFrame> | undefined;
  private disposed = false;
  private visible = true;
  private prepared = false;
  private startupSteps = 0;
  private lastFrame = 0;
  private accumulator = 0;
  private inFlight = false;
  private appliedSequence = 0;
  private forceFull = false;
  private lastGridSample = 0;
  private staticFrameSent = false;
  private initialFrameSent = false;
  private visibleAscii = false;
  private readySent = false;
  private readbackValidated = false;
  private textPointer = {
    x: -1000,
    y: -1000,
    active: false,
    points: [] as Array<[number, number]>,
    last: 0,
  };
  private pixels: Uint8Array;
  private stepsThisFrame = 0;
  private frameCounter = 0;
  private sampleGpuThisFrame = false;
  private gpuQueryStartedThisFrame = false;
  private readonly readbackSlots: ReadbackSlot[] = [];
  private readbackCursor = 0;
  private pendingReadbackSlot: ReadbackSlot | undefined;
  private pendingGrid: PendingGrid | undefined;
  private completedGrid: PendingGrid | undefined;
  private pboReadbackEnabled = false;

  constructor(message: WorkerInit) {
    this.perfEnabled = message.perfEnabled;
    this.canvas = message.canvas;
    this.viewportWidth = message.viewportWidth;
    this.width = Math.max(1, message.width);
    this.height = Math.max(1, message.height);
    this.pixelRatio = message.pixelRatio;
    this.coverage = message.coverage;
    this.mountEpoch = message.mountEpoch;
    this.charWidth = message.charWidth;
    this.lineHeight = message.lineHeight;
    this.reducedMotion = message.reducedMotion;
    this.state.paused = message.reducedMotion;
    this.prewarm = message.prewarm === true;
    // Ordinary workers belong to an already-visible creation surface. Keep
    // their cold warmup below the viewport from its first simulation step;
    // hidden prewarm workers are trimmed when they are borrowed instead.
    this.activated = !this.prewarm;
    this.seed = Math.random() * 500;
    this.windTime = this.seed;
    this.simulationWidth = this.viewportWidth < 600 ? 192 : 320;
    this.simulationHeight = this.simulationWidth * 2;
    this.densityWidth = this.simulationWidth * 4;
    this.densityHeight = this.simulationHeight * 4;
    this.context = this.createContext();
    this.gpuTimerExtension = this.perfEnabled
      ? (this.context.getExtension(
          "EXT_disjoint_timer_query_webgl2",
        ) as GpuTimerExtension | null)
      : null;
    const debugRenderer = this.perfEnabled
      ? (this.context.getExtension("WEBGL_debug_renderer_info") as {
          UNMASKED_RENDERER_WEBGL: number;
          UNMASKED_VENDOR_WEBGL: number;
        } | null)
      : null;
    this.renderer = String(
      this.context.getParameter(
        debugRenderer?.UNMASKED_RENDERER_WEBGL ?? this.context.RENDERER,
      ),
    );
    this.vendor = String(
      this.context.getParameter(
        debugRenderer?.UNMASKED_VENDOR_WEBGL ?? this.context.VENDOR,
      ),
    );
    if (debugRenderer) {
      this.unmaskedRenderer = String(
        this.context.getParameter(debugRenderer.UNMASKED_RENDERER_WEBGL),
      );
      this.unmaskedVendor = String(
        this.context.getParameter(debugRenderer.UNMASKED_VENDOR_WEBGL),
      );
    }
    const rgba16f: FloatTextureFormat = {
      name: "rgba16f",
      internalFormat: this.context.RGBA16F,
      format: this.context.RGBA,
      bytesPerPixel: 8,
    };
    const r16f: FloatTextureFormat = {
      name: "r16f",
      internalFormat: this.context.R16F,
      format: this.context.RED,
      bytesPerPixel: 2,
    };
    const rg16f: FloatTextureFormat = {
      name: "rg16f",
      internalFormat: this.context.RG16F,
      format: this.context.RG,
      bytesPerPixel: 4,
    };
    this.scalarFormat = this.probeFloatFormat(r16f) ? r16f : rgba16f;
    this.velocityFormat = this.probeFloatFormat(rg16f) ? rg16f : rgba16f;
    this.grid = createAsciiGrid(message.gridWidth, message.gridHeight, {
      chunkWidth: 64,
    });
    this.pixels = new Uint8Array(this.grid.area * 4);
    this.canvas.width = Math.max(1, message.canvasWidth);
    this.canvas.height = Math.max(1, message.canvasHeight);
    this.setupPrograms();
    this.velocity = this.createDoubleTarget(
      this.simulationWidth,
      this.simulationHeight,
      this.velocityFormat,
    );
    this.ink = this.createDoubleTarget(
      this.densityWidth,
      this.densityHeight,
      this.scalarFormat,
    );
    this.pressure = this.createDoubleTarget(
      this.simulationWidth,
      this.simulationHeight,
      this.scalarFormat,
    );
    this.divergence = this.createTarget(
      this.simulationWidth,
      this.simulationHeight,
      false,
      this.scalarFormat,
    );
    this.curl = this.createTarget(
      this.simulationWidth,
      this.simulationHeight,
      false,
      this.scalarFormat,
    );
    this.sampleTarget = this.createTarget(
      message.gridWidth,
      message.gridHeight,
      true,
    );
    this.trail = new Float32Array(this.grid.area);
    this.resetReadbackRing();
  }

  private createContext(): WebGL2RenderingContext {
    const context = this.canvas.getContext("webgl2", {
      alpha: false,
      antialias: false,
      powerPreference: "high-performance",
    }) as WebGL2RenderingContext | null;
    if (!context || !context.getExtension("EXT_color_buffer_float"))
      throw new Error("WebGL 2 is required for the smoke study.");
    return context;
  }

  private probeFloatFormat(format: FloatTextureFormat): boolean {
    const texture = this.context.createTexture();
    const framebuffer = this.context.createFramebuffer();
    if (!texture || !framebuffer) {
      if (texture) this.context.deleteTexture(texture);
      if (framebuffer) this.context.deleteFramebuffer(framebuffer);
      return false;
    }
    try {
      this.context.bindTexture(this.context.TEXTURE_2D, texture);
      this.context.texParameteri(
        this.context.TEXTURE_2D,
        this.context.TEXTURE_MIN_FILTER,
        this.context.LINEAR,
      );
      this.context.texParameteri(
        this.context.TEXTURE_2D,
        this.context.TEXTURE_MAG_FILTER,
        this.context.LINEAR,
      );
      this.context.texParameteri(
        this.context.TEXTURE_2D,
        this.context.TEXTURE_WRAP_S,
        this.context.CLAMP_TO_EDGE,
      );
      this.context.texParameteri(
        this.context.TEXTURE_2D,
        this.context.TEXTURE_WRAP_T,
        this.context.CLAMP_TO_EDGE,
      );
      this.context.texImage2D(
        this.context.TEXTURE_2D,
        0,
        format.internalFormat,
        1,
        1,
        0,
        format.format,
        this.context.HALF_FLOAT,
        null,
      );
      this.context.bindFramebuffer(this.context.FRAMEBUFFER, framebuffer);
      this.context.framebufferTexture2D(
        this.context.FRAMEBUFFER,
        this.context.COLOR_ATTACHMENT0,
        this.context.TEXTURE_2D,
        texture,
        0,
      );
      return (
        this.context.checkFramebufferStatus(this.context.FRAMEBUFFER) ===
        this.context.FRAMEBUFFER_COMPLETE
      );
    } finally {
      this.context.bindFramebuffer(this.context.FRAMEBUFFER, null);
      this.context.deleteFramebuffer(framebuffer);
      this.context.deleteTexture(texture);
    }
  }

  private setupPrograms() {
    const vertex = `#version 300 es
      in vec2 position; out vec2 uv;
      void main(){uv=position*.5+.5;gl_Position=vec4(position,0.,1.);}`;
    const common = `#version 300 es
      precision highp float; in vec2 uv; out vec4 color;
      uniform vec2 texel; uniform float dt; uniform float time;
      uniform sampler2D velocity; uniform sampler2D density;
      uniform sampler2D pressure; uniform sampler2D curl;
      uniform sampler2D divergence; uniform sampler2D source;`;
    const shader = (type: number, source: string): WebGLShader => {
      const compiled = this.context.createShader(type);
      if (!compiled) throw new Error("Unable to create a smoke shader.");
      this.context.shaderSource(compiled, source);
      this.context.compileShader(compiled);
      if (
        !this.context.getShaderParameter(compiled, this.context.COMPILE_STATUS)
      ) {
        const info =
          this.context.getShaderInfoLog(compiled) ||
          "Smoke shader compilation failed.";
        this.context.deleteShader(compiled);
        throw new Error(info);
      }
      return compiled;
    };
    const makeProgram = (
      fragment: string,
      vertexSource = vertex,
    ): ShaderProgram => {
      const vertexShader = shader(this.context.VERTEX_SHADER, vertexSource);
      const fragmentShader = shader(
        this.context.FRAGMENT_SHADER,
        common + fragment,
      );
      const program = this.context.createProgram();
      if (!program) throw new Error("Unable to create a smoke program.");
      this.context.attachShader(program, vertexShader);
      this.context.attachShader(program, fragmentShader);
      this.context.linkProgram(program);
      this.context.deleteShader(vertexShader);
      this.context.deleteShader(fragmentShader);
      if (
        !this.context.getProgramParameter(program, this.context.LINK_STATUS)
      ) {
        const info =
          this.context.getProgramInfoLog(program) ||
          "Smoke program linking failed.";
        this.context.deleteProgram(program);
        throw new Error(info);
      }
      const uniforms: Record<string, WebGLUniformLocation | null> = {};
      const count = this.context.getProgramParameter(
        program,
        this.context.ACTIVE_UNIFORMS,
      ) as number;
      for (let index = 0; index < count; index++) {
        const info = this.context.getActiveUniform(program, index);
        if (info)
          uniforms[info.name] = this.context.getUniformLocation(
            program,
            info.name,
          );
      }
      const result = { program, uniforms };
      this.programs.push(result);
      return result;
    };

    const bilinear =
      "vec4 sampleLinear(sampler2D s,vec2 p){return texture(s,p);}";
    const wandering = `
      float random1(float x){return fract(sin(x*127.1+31.7)*43758.5453);}
      float wandering(float x){float i=floor(x),f=fract(x);f=f*f*(3.-2.*f);return mix(random1(i),random1(i+1.),f)*2.-1.;}
      vec2 emitter(){return vec2(.5+sin(time*.43)*.004,.044);}`;
    const windVertex = `#version 300 es
      in vec2 position; out vec2 uv; flat out highp float wind;
      uniform float windTime;
      float random1(float x){return fract(sin(x*127.1+31.7)*43758.5453);}
      float wandering(float x){float i=floor(x),f=fract(x);f=f*f*(3.-2.*f);return mix(random1(i),random1(i+1.),f)*2.-1.;}
      void main(){uv=position*.5+.5;wind=wandering(windTime*.16+41.)*46.+wandering(windTime*.36+137.)*14.;gl_Position=vec4(position,0.,1.);}`;
    const hoistedFlow = `${bilinear}${wandering}flat in highp float wind;uniform float windTime;
      vec2 flow(vec2 p){vec2 v=sampleLinear(velocity,p).xy;v.y+=.125/texel.y;
      float shear=wandering(p.y*2.5-windTime*.12+87.)*7.;
      v.x+=(wind+shear)*smoothstep(.06,.55,p.y)*(.00390625/texel.x);return v;}`;
    this.advect = makeProgram(
      `${hoistedFlow}uniform float dissipation;
      void main(){vec2 v=flow(uv);color=sampleLinear(source,uv-dt*v*texel)*exp(-dissipation*dt);}`,
      windVertex,
    );
    this.advectSmoke = makeProgram(
      `${hoistedFlow}uniform float dissipation;
      void main(){vec2 p=uv-dt*flow(uv)*texel;float d=sampleLinear(source,p).x;
      vec2 spread=texel*vec2(.75,1.5);
      float surrounding=(sampleLinear(source,p+vec2(spread.x,0)).x+sampleLinear(source,p-vec2(spread.x,0)).x+
        sampleLinear(source,p+vec2(0,spread.y)).x+sampleLinear(source,p-vec2(0,spread.y)).x)*.25;
      d=mix(d,surrounding,1.-exp(-14.*dt));color=vec4(d*exp(-dissipation*dt),0,0,1);}`,
      windVertex,
    );
    this.forces = makeProgram(`${wandering}
      void main(){vec2 v=texture(velocity,uv).xy;float d=texture(density,uv).x;
      float height=smoothstep(.09,.65,uv.y);float breeze=sin(uv.y*10.-time*.64)*7.+sin(uv.y*23.+time*.37)*4.;
      float scale=.00390625/texel.x;v.x+=dt*(breeze*height+sin(time*1.8)*.8)*(.12+min(d,1.))*scale;
      v.y+=dt*d*60.*scale;vec2 delta=(uv-emitter())*vec2(1.,1.8);float jet=exp(-dot(delta,delta)/.00009);
      v=mix(v,vec2(2.*sin(time*1.5)+sin(time*3.1),62.+6.*sin(time*.8))*scale,min(.95,jet*.5));
      if(uv.x<texel.x||uv.x>1.-texel.x)v.x=0.;if(uv.y<texel.y)v.y=0.;color=vec4(v,0,1);}`);
    this.inject =
      makeProgram(`${wandering}uniform float amount;void main(){float d=texture(density,uv).x;
      vec2 delta=(uv-emitter())*vec2(.85,1.8);float jet=exp(-dot(delta,delta)/.000075);
      d+=jet*dt*amount*9.2*(.78+.22*sin(time*2.4));d*=1.-smoothstep(.86,1.,uv.y)*dt*1.4;color=vec4(d,0,0,1);}`);
    this.curlProgram =
      makeProgram(`void main(){float l=texture(velocity,uv-vec2(texel.x,0)).y;
      float r=texture(velocity,uv+vec2(texel.x,0)).y;float b=texture(velocity,uv-vec2(0,texel.y)).x;
      float t=texture(velocity,uv+vec2(0,texel.y)).x;color=vec4(.5*(r-l-t+b),0,0,1);}`);
    this.confine =
      makeProgram(`void main(){float l=abs(texture(curl,uv-vec2(texel.x,0)).x);
      float r=abs(texture(curl,uv+vec2(texel.x,0)).x);float b=abs(texture(curl,uv-vec2(0,texel.y)).x);
      float t=abs(texture(curl,uv+vec2(0,texel.y)).x);float c=texture(curl,uv).x;vec2 f=.5*vec2(t-b,r-l);
      f/=length(f)+.0001;f*=10.*c;f.y*=-1.;color=vec4(clamp(texture(velocity,uv).xy+dt*f,vec2(-160.),vec2(160.)),0,1);}`);
    this.divergenceProgram =
      makeProgram(`void main(){float l=texture(velocity,uv-vec2(texel.x,0)).x;
      float r=texture(velocity,uv+vec2(texel.x,0)).x;float b=texture(velocity,uv-vec2(0,texel.y)).y;
      float t=texture(velocity,uv+vec2(0,texel.y)).y;color=vec4(.5*(r-l+t-b),0,0,1);}`);
    this.pressureProgram =
      makeProgram(`void main(){float l=texture(pressure,uv-vec2(texel.x,0)).x;
      float r=texture(pressure,uv+vec2(texel.x,0)).x;float b=texture(pressure,uv-vec2(0,texel.y)).x;
      float t=texture(pressure,uv+vec2(0,texel.y)).x;color=vec4((l+r+b+t-texture(divergence,uv).x)*.25,0,0,1);}`);
    this.gradient =
      makeProgram(`void main(){float l=texture(pressure,uv-vec2(texel.x,0)).x;
      float r=texture(pressure,uv+vec2(texel.x,0)).x;float b=texture(pressure,uv-vec2(0,texel.y)).x;
      float t=texture(pressure,uv+vec2(0,texel.y)).x;color=vec4(texture(velocity,uv).xy-.5*vec2(r-l,t-b),0,1);}`);
    const smokeField = `${bilinear}
      uniform vec2 resolution;uniform float zoom;
      float hash(vec2 p){return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453);}
      vec2 domain(vec2 p){float aspect=resolution.x/resolution.y;float fit=min(1.65,aspect*1.35);
        vec2 q=vec2((.5-p.x)*aspect/fit+.5,(p.y-.038)/1.16);vec2 focus=vec2(.565,.18);return focus+(q-focus)/zoom;}
      float smoke(vec2 p){vec2 q=domain(p);if(q.x<0.||q.x>1.||q.y<0.||q.y>1.)return 0.;
        float d=sampleLinear(density,q).x;vec2 cell=1./vec2(textureSize(density,0));
        float dx=(sampleLinear(density,q+vec2(cell.x,0)).x-sampleLinear(density,q-vec2(cell.x,0)).x)*texel.x/cell.x;
        float light=.80+.20*tanh(dx*4.);return clamp((1.-exp(-d*6.5))*light,0.,1.);}`;
    this.display = makeProgram(`${smokeField}uniform float asciiOn;
      void main(){vec2 pixel=uv*resolution;float raw=smoke(uv);float value=asciiOn>.5?raw*.025:raw;
        float grain=(hash(pixel+floor(time*20.))-.5)*.007;float vignette=1.-.24*length((uv-.5)*vec2(1.,.7));
        vec3 bg=vec3(.018,.022,.032);vec3 tint=mix(vec3(.56,.60,.63),vec3(.93,.90,.84),smoothstep(.08,.75,raw));
        color=vec4(bg+tint*value*vignette+vec3(grain),1.);}`);
    this.sampleAscii = makeProgram(`${smokeField}uniform vec2 coverage;
      void main(){float d=smoke(uv*coverage+vec2(0.,1.-coverage.y));color=vec4(d,d,d,1.);}`);
    this.trimDensity = makeProgram(`uniform float cutoff;uniform float edge;
      void main(){float inkValue=texture(density,uv).x;
      float above=smoothstep(cutoff-edge,cutoff+edge,uv.y);
      color=vec4(inkValue*(1.-above),0.,0.,1.);}`);

    const buffer = this.context.createBuffer();
    const vertexArray = this.context.createVertexArray();
    if (!buffer || !vertexArray)
      throw new Error("Unable to create smoke geometry.");
    this.context.bindBuffer(this.context.ARRAY_BUFFER, buffer);
    this.context.bufferData(
      this.context.ARRAY_BUFFER,
      new Float32Array([-1, -1, 1, -1, -1, 1, -1, 1, 1, -1, 1, 1]),
      this.context.STATIC_DRAW,
    );
    this.context.bindVertexArray(vertexArray);
    this.context.enableVertexAttribArray(0);
    this.context.vertexAttribPointer(0, 2, this.context.FLOAT, false, 0, 0);
    this.vertexArray = vertexArray;
    this.buffer = buffer;
  }

  private buffer!: WebGLBuffer;
  private vertexArray!: WebGLVertexArrayObject;

  private createTarget(
    width = this.simulationWidth,
    height = this.simulationHeight,
    byteReadable = false,
    floatFormat?: FloatTextureFormat,
  ): RenderTarget {
    const texture = this.context.createTexture();
    const framebuffer = this.context.createFramebuffer();
    if (!texture || !framebuffer)
      throw new Error("Unable to create smoke target.");
    const targetFormat = byteReadable
      ? {
          internalFormat: this.context.RGBA8,
          format: this.context.RGBA,
          bytesPerPixel: 4,
        }
      : (floatFormat ?? this.scalarFormat);
    this.context.bindTexture(this.context.TEXTURE_2D, texture);
    for (const [parameter, value] of [
      [
        this.context.TEXTURE_MIN_FILTER,
        byteReadable ? this.context.NEAREST : this.context.LINEAR,
      ],
      [
        this.context.TEXTURE_MAG_FILTER,
        byteReadable ? this.context.NEAREST : this.context.LINEAR,
      ],
      [this.context.TEXTURE_WRAP_S, this.context.CLAMP_TO_EDGE],
      [this.context.TEXTURE_WRAP_T, this.context.CLAMP_TO_EDGE],
    ] as const)
      this.context.texParameteri(this.context.TEXTURE_2D, parameter, value);
    this.context.texImage2D(
      this.context.TEXTURE_2D,
      0,
      targetFormat.internalFormat,
      width,
      height,
      0,
      targetFormat.format,
      byteReadable ? this.context.UNSIGNED_BYTE : this.context.HALF_FLOAT,
      null,
    );
    this.context.bindFramebuffer(this.context.FRAMEBUFFER, framebuffer);
    this.context.framebufferTexture2D(
      this.context.FRAMEBUFFER,
      this.context.COLOR_ATTACHMENT0,
      this.context.TEXTURE_2D,
      texture,
      0,
    );
    if (
      this.context.checkFramebufferStatus(this.context.FRAMEBUFFER) !==
      this.context.FRAMEBUFFER_COMPLETE
    )
      throw new Error("Smoke rendering is unavailable on this device.");
    this.context.viewport(0, 0, width, height);
    this.context.clearColor(0, 0, 0, 0);
    this.context.clear(this.context.COLOR_BUFFER_BIT);
    const target = { texture, framebuffer, width, height };
    const textureBytes = width * height * targetFormat.bytesPerPixel;
    if (this.perfEnabled) this.textureBytes += textureBytes;
    const measuredTarget = { ...target, textureBytes };
    this.renderTargets.push(measuredTarget);
    return measuredTarget;
  }

  private releaseReadbackRing() {
    for (const slot of this.readbackSlots) {
      if (slot.fence) this.context.deleteSync(slot.fence);
      this.context.deleteBuffer(slot.buffer);
    }
    this.readbackSlots.length = 0;
    this.readbackCursor = 0;
    this.pendingReadbackSlot = undefined;
    this.pendingGrid = undefined;
    this.completedGrid = undefined;
    this.pboReadbackEnabled = false;
  }

  private resetReadbackRing() {
    this.releaseReadbackRing();
    if (
      typeof this.context.getBufferSubData !== "function" ||
      typeof this.context.fenceSync !== "function" ||
      typeof this.context.clientWaitSync !== "function"
    )
      return;
    const bytes = this.grid.area * 4;
    for (let index = 0; index < 3; index++) {
      const buffer = this.context.createBuffer();
      if (!buffer) break;
      this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, buffer);
      this.context.bufferData(
        this.context.PIXEL_PACK_BUFFER,
        bytes,
        this.context.STREAM_READ,
      );
      this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, null);
      this.readbackSlots.push({ buffer, fence: null });
    }
    if (!this.readbackSlots.length) this.releaseReadbackRing();
    else this.pboReadbackEnabled = true;
  }

  private disableReadbackPbo() {
    this.releaseReadbackRing();
  }

  private createDoubleTarget(
    width = this.simulationWidth,
    height = this.simulationHeight,
    format?: FloatTextureFormat,
  ): DoubleTarget {
    const target = {
      read: this.createTarget(width, height, false, format),
      write: this.createTarget(width, height, false, format),
      swap() {
        [target.read, target.write] = [target.write, target.read];
      },
    } as DoubleTarget;
    return target;
  }

  private draw(
    shader: ShaderProgram,
    destination: RenderTarget | null,
    values: Record<string, number | [number, number]> = {},
    textures: Record<string, WebGLTexture | RenderTarget> = {},
  ) {
    this.context.useProgram(shader.program);
    this.context.bindVertexArray(this.vertexArray);
    this.context.bindFramebuffer(
      this.context.FRAMEBUFFER,
      destination?.framebuffer ?? null,
    );
    this.context.viewport(
      0,
      0,
      destination?.width ?? this.canvas.width,
      destination?.height ?? this.canvas.height,
    );
    if (shader.uniforms.texel)
      this.context.uniform2f(
        shader.uniforms.texel,
        1 / this.simulationWidth,
        1 / this.simulationHeight,
      );
    for (const [name, value] of Object.entries(values)) {
      const location = shader.uniforms[name];
      if (!location) continue;
      if (Array.isArray(value)) this.context.uniform2fv(location, value);
      else this.context.uniform1f(location, value);
    }
    let unit = 0;
    for (const [name, value] of Object.entries(textures)) {
      const location = shader.uniforms[name];
      if (!location) continue;
      this.context.activeTexture(this.context.TEXTURE0 + unit);
      this.context.bindTexture(
        this.context.TEXTURE_2D,
        "texture" in value ? value.texture : value,
      );
      this.context.uniform1i(location, unit++);
    }
    this.context.drawArrays(this.context.TRIANGLES, 0, 6);
  }

  private recordTiming(target: RollingTimings, started: number) {
    if (this.perfEnabled) target.add(performance.now() - started);
  }

  private beginGpuTimer() {
    if (
      !this.perfEnabled ||
      !this.sampleGpuThisFrame ||
      this.gpuQueryStartedThisFrame ||
      !this.gpuTimerExtension ||
      this.pendingGpuQueries.length >= 8
    )
      return undefined;
    const query = this.context.createQuery();
    if (!query) return undefined;
    this.context.beginQuery(this.gpuTimerExtension.TIME_ELAPSED_EXT, query);
    this.gpuQueryStartedThisFrame = true;
    return query;
  }

  private endGpuTimer(query: WebGLQuery | undefined) {
    if (!query || !this.gpuTimerExtension) return;
    this.context.endQuery(this.gpuTimerExtension.TIME_ELAPSED_EXT);
    this.pendingGpuQueries.push(query);
  }

  private pollGpuTimers() {
    if (!this.gpuTimerExtension || !this.pendingGpuQueries.length) return;
    let hasAvailableQuery = false;
    for (const query of this.pendingGpuQueries) {
      if (
        this.context.getQueryParameter(
          query,
          this.context.QUERY_RESULT_AVAILABLE,
        )
      ) {
        hasAvailableQuery = true;
        break;
      }
    }
    if (!hasAvailableQuery) return;
    const disjoint = Boolean(
      this.context.getParameter(this.gpuTimerExtension.GPU_DISJOINT_EXT),
    );
    for (let index = this.pendingGpuQueries.length - 1; index >= 0; index--) {
      const query = this.pendingGpuQueries[index];
      if (
        !this.context.getQueryParameter(
          query,
          this.context.QUERY_RESULT_AVAILABLE,
        )
      )
        continue;
      if (!disjoint) {
        const nanoseconds = Number(
          this.context.getQueryParameter(query, this.context.QUERY_RESULT),
        );
        if (Number.isFinite(nanoseconds))
          this.gpuTimings.add(nanoseconds / 1e6);
      }
      this.context.deleteQuery(query);
      this.pendingGpuQueries.splice(index, 1);
    }
  }

  private collectMetrics(now: number): SmokeWorkerMetrics | undefined {
    if (!this.perfEnabled || now - this.lastMetricsPublish < 250)
      return undefined;
    this.lastMetricsPublish = now;
    return {
      windowMs: this.timingWindowMs,
      cpuStep: this.cpuStepTimings.summary(now),
      stepCount: this.stepCountTimings.summary(now),
      sample: this.sampleTimings.summary(now),
      grid: this.gridTimings.summary(now),
      gpu: this.gpuTimings.summary(now),
      gpuTimer: this.gpuTimerExtension !== null,
      textureBytes: this.textureBytes,
      canvasWidth: this.canvas.width,
      canvasHeight: this.canvas.height,
      gridWidth: this.grid.width,
      gridHeight: this.grid.height,
      gridCells: this.grid.area,
      pixelRatio: this.pixelRatio,
      renderer: this.renderer,
      vendor: this.vendor,
      unmaskedRenderer: this.unmaskedRenderer,
      unmaskedVendor: this.unmaskedVendor,
      scalarFormat: this.scalarFormat.name,
      velocityFormat: this.velocityFormat.name,
      readbackMode: this.pboReadbackEnabled ? "pbo" : "direct",
    };
  }

  resize(message: WorkerResize) {
    this.viewportWidth = message.viewportWidth;
    this.width = Math.max(1, message.width);
    this.height = Math.max(1, message.height);
    this.pixelRatio = message.pixelRatio;
    this.coverage = message.coverage;
    this.charWidth = message.charWidth;
    this.lineHeight = message.lineHeight;
    this.canvas.width = Math.max(1, message.canvasWidth);
    this.canvas.height = Math.max(1, message.canvasHeight);
    this.grid.resize(message.gridWidth, message.gridHeight);
    this.pixels = new Uint8Array(this.grid.area * 4);
    this.trail = new Float32Array(this.grid.area);
    this.resetReadbackRing();
    if (this.perfEnabled) this.textureBytes -= this.sampleTarget.textureBytes;
    this.context.deleteFramebuffer(this.sampleTarget.framebuffer);
    this.context.deleteTexture(this.sampleTarget.texture);
    this.sampleTarget = this.createTarget(
      message.gridWidth,
      message.gridHeight,
      true,
    );
    this.forceFull = true;
    this.staticFrameSent = false;
    this.initialFrameSent = false;
    this.visibleAscii = false;
    this.readbackValidated = false;
    if (this.visible && !this.animationFrame) this.start();
  }

  pointer(message: WorkerPointer) {
    const wasActive = this.textPointer.active;
    this.textPointer.active = message.active;
    if (!message.active) return;
    const steps = wasActive
      ? Math.min(
          32,
          Math.max(
            1,
            Math.ceil(
              Math.hypot(
                message.x - this.textPointer.x,
                message.y - this.textPointer.y,
              ) / 16,
            ),
          ),
        )
      : 1;
    for (let index = 1; index <= steps; index++)
      this.textPointer.points.push(
        wasActive
          ? [
              this.textPointer.x +
                ((message.x - this.textPointer.x) * index) / steps,
              this.textPointer.y +
                ((message.y - this.textPointer.y) * index) / steps,
            ]
          : [message.x, message.y],
      );
    this.textPointer.points = this.textPointer.points.slice(-128);
    this.textPointer.x = message.x;
    this.textPointer.y = message.y;
  }

  visibility(visible: boolean) {
    this.visible = visible;
    if (!visible) {
      if (this.animationFrame) cancelFrame(this.animationFrame);
      this.animationFrame = undefined;
      this.lastFrame = 0;
      this.accumulator = 0;
      this.textPointer.points.length = 0;
    } else if (
      !this.animationFrame &&
      !(this.reducedMotion && this.staticFrameSent)
    )
      this.start();
  }

  motion(reducedMotion: boolean) {
    this.reducedMotion = reducedMotion;
    this.state.paused = reducedMotion;
    this.resetReadbackRing();
    this.readbackValidated = false;
    this.staticFrameSent = false;
    this.forceFull = true;
    if (!this.animationFrame && this.visible) this.start();
    if (reducedMotion) {
      this.lastFrame = 0;
      this.accumulator = 0;
    }
  }

  activate(mountEpoch: number, reducedMotion: boolean) {
    this.mountEpoch = mountEpoch;
    this.visible = true;
    this.reducedMotion = reducedMotion;
    this.state.paused = reducedMotion;
    this.activated = true;
    this.forceFull = true;
    this.staticFrameSent = false;
    this.initialFrameSent = false;
    this.visibleAscii = false;
    this.readySent = false;
    this.inFlight = false;
    this.appliedSequence = 0;
    this.lastGridSample = 0;
    this.lastFrame = 0;
    this.accumulator = 0;
    this.textPointer.active = false;
    this.textPointer.points.length = 0;
    this.textPointer.last = 0;
    this.resetReadbackRing();
    this.readbackValidated = false;
    if (!reducedMotion) this.trimInkAboveViewport();
    scope.postMessage({ type: "activated" });
    this.start();
  }

  acknowledge(sequence: number) {
    if (sequence >= this.appliedSequence) {
      this.appliedSequence = sequence;
      this.inFlight = false;
      this.finishCompletedGrid();
    }
  }

  requestFull() {
    this.forceFull = true;
  }

  start() {
    if (this.animationFrame || this.disposed) return;
    this.animationFrame = requestFrame((now) => this.frame(now));
  }

  private trimInkAboveViewport() {
    const edge = 1 / Math.max(1, this.densityHeight);
    this.draw(
      this.trimDensity,
      this.ink.write,
      {
        cutoff: CAMERA_BOTTOM_CUTOFF_ZOOM10 - edge,
        edge,
      },
      { density: this.ink.read },
    );
    this.ink.swap();
  }

  private step(dt: number, wallDt = dt) {
    const started = this.perfEnabled ? performance.now() : 0;
    const gpuQuery = this.beginGpuTimer();
    this.simulationTime += dt;
    this.windTime += wallDt;
    const values = {
      dt,
      time: this.simulationTime + this.seed,
      windTime: this.windTime,
    };
    this.draw(
      this.advect,
      this.velocity.write,
      { ...values, dissipation: 0.13 },
      { velocity: this.velocity.read, source: this.velocity.read },
    );
    this.velocity.swap();
    this.draw(this.forces, this.velocity.write, values, {
      velocity: this.velocity.read,
      density: this.ink.read,
    });
    this.velocity.swap();
    this.draw(this.curlProgram, this.curl, values, {
      velocity: this.velocity.read,
    });
    this.draw(this.confine, this.velocity.write, values, {
      velocity: this.velocity.read,
      curl: this.curl,
    });
    this.velocity.swap();
    this.draw(this.divergenceProgram, this.divergence, values, {
      velocity: this.velocity.read,
    });
    for (let index = 0; index < 16; index++) {
      this.draw(this.pressureProgram, this.pressure.write, values, {
        pressure: this.pressure.read,
        divergence: this.divergence,
      });
      this.pressure.swap();
    }
    this.draw(this.gradient, this.velocity.write, values, {
      velocity: this.velocity.read,
      pressure: this.pressure.read,
    });
    this.velocity.swap();
    this.draw(
      this.advectSmoke,
      this.ink.write,
      { ...values, dissipation: 0.055 },
      { velocity: this.velocity.read, source: this.ink.read },
    );
    this.ink.swap();
    this.draw(
      this.inject,
      this.ink.write,
      { ...values, amount: this.state.density },
      { density: this.ink.read },
    );
    this.ink.swap();
    this.endGpuTimer(gpuQuery);
    if (this.perfEnabled) this.recordTiming(this.cpuStepTimings, started);
  }

  private readAsciiPixels(width: number, height: number) {
    // The sampler target is deliberately RGBA8: unlike the fluid RGBA16F
    // targets, WebGL guarantees a byte-compatible readback for this path.
    this.context.readPixels(
      0,
      0,
      width,
      height,
      this.context.RGBA,
      this.context.UNSIGNED_BYTE,
      this.pixels,
    );
    if (!this.readbackValidated) {
      const error = this.context.getError();
      if (error !== this.context.NO_ERROR)
        throw new Error("Smoke ASCII readback failed.");
      this.readbackValidated = true;
    }
  }

  private queueReadback(pending: PendingGrid): "ready" | "pending" {
    if (!this.pboReadbackEnabled) {
      this.context.bindFramebuffer(
        this.context.FRAMEBUFFER,
        this.sampleTarget.framebuffer,
      );
      this.readAsciiPixels(pending.sampleWidth, pending.sampleHeight);
      if (this.perfEnabled)
        this.recordTiming(this.sampleTimings, pending.sampleStarted);
      return "ready";
    }

    let slot: ReadbackSlot | undefined;
    let slotIndex = this.readbackCursor;
    for (let offset = 0; offset < this.readbackSlots.length; offset++) {
      const index = (this.readbackCursor + offset) % this.readbackSlots.length;
      const candidate = this.readbackSlots[index];
      if (candidate && !candidate.fence) {
        slot = candidate;
        slotIndex = index;
        break;
      }
    }
    if (!slot) {
      this.disableReadbackPbo();
      this.context.bindFramebuffer(
        this.context.FRAMEBUFFER,
        this.sampleTarget.framebuffer,
      );
      this.readAsciiPixels(pending.sampleWidth, pending.sampleHeight);
      if (this.perfEnabled)
        this.recordTiming(this.sampleTimings, pending.sampleStarted);
      return "ready";
    }

    this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, slot.buffer);
    this.context.readPixels(
      0,
      0,
      pending.sampleWidth,
      pending.sampleHeight,
      this.context.RGBA,
      this.context.UNSIGNED_BYTE,
      0,
    );
    this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, null);
    if (!this.readbackValidated) {
      const error = this.context.getError();
      if (error !== this.context.NO_ERROR) {
        this.disableReadbackPbo();
        this.context.bindFramebuffer(
          this.context.FRAMEBUFFER,
          this.sampleTarget.framebuffer,
        );
        this.readAsciiPixels(pending.sampleWidth, pending.sampleHeight);
        if (this.perfEnabled)
          this.recordTiming(this.sampleTimings, pending.sampleStarted);
        return "ready";
      }
      this.readbackValidated = true;
    }
    const fence = this.context.fenceSync(
      this.context.SYNC_GPU_COMMANDS_COMPLETE,
      0,
    );
    if (!fence) {
      this.disableReadbackPbo();
      this.context.bindFramebuffer(
        this.context.FRAMEBUFFER,
        this.sampleTarget.framebuffer,
      );
      this.readAsciiPixels(pending.sampleWidth, pending.sampleHeight);
      if (this.perfEnabled)
        this.recordTiming(this.sampleTimings, pending.sampleStarted);
      return "ready";
    }
    slot.fence = fence;
    this.pendingReadbackSlot = slot;
    this.pendingGrid = pending;
    this.readbackCursor = (slotIndex + 1) % this.readbackSlots.length;
    if (this.perfEnabled)
      pending.sampleCpuMs = performance.now() - pending.sampleStarted;
    this.context.flush();
    return "pending";
  }

  private pollReadback(): PendingGrid | undefined {
    const slot = this.pendingReadbackSlot;
    const pending = this.pendingGrid;
    if (!slot || !slot.fence || !pending) return undefined;
    const status = this.context.clientWaitSync(slot.fence, 0, 0);
    if (
      status !== this.context.ALREADY_SIGNALED &&
      status !== this.context.CONDITION_SATISFIED
    ) {
      if (status === this.context.WAIT_FAILED) {
        this.disableReadbackPbo();
      }
      return undefined;
    }
    try {
      const copyStarted = this.perfEnabled ? performance.now() : 0;
      this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, slot.buffer);
      this.context.getBufferSubData(
        this.context.PIXEL_PACK_BUFFER,
        0,
        this.pixels,
      );
      this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, null);
      if (this.perfEnabled)
        this.sampleTimings.add(
          (pending.sampleCpuMs ?? 0) + performance.now() - copyStarted,
        );
    } catch {
      this.context.bindBuffer(this.context.PIXEL_PACK_BUFFER, null);
      this.disableReadbackPbo();
      return undefined;
    }
    this.context.deleteSync(slot.fence);
    slot.fence = null;
    this.pendingReadbackSlot = undefined;
    this.pendingGrid = undefined;
    if (this.inFlight) {
      this.completedGrid = pending;
      return undefined;
    }
    return pending;
  }

  private finishGrid(pending: PendingGrid): boolean {
    const gridStarted = performance.now();
    const frame = this.grid.render(this.pixels, this.trail, pending.now, {
      appliedSequence: this.appliedSequence,
      forceFull: this.forceFull,
    });
    if (this.perfEnabled) this.gridTimings.add(performance.now() - gridStarted);
    this.forceFull = false;
    this.lastGridSample = pending.now;
    if (!frame.changed) return false;
    this.inFlight = true;
    if (frame.full && frame.fullStrings)
      this.visibleAscii = frame.fullStrings.some((text) => /\S/.test(text));
    else if (!this.visibleAscii)
      this.visibleAscii = frame.patches.some((patch) => /\S/.test(patch.text));
    const metrics = this.collectMetrics(pending.now);
    scope.postMessage({
      type: "frame",
      frame,
      computeMs:
        Math.round((pending.issuedAt - pending.computeStarted) * 100) / 100,
      gridMs: Math.round((performance.now() - gridStarted) * 100) / 100,
      ...(metrics ? { metrics } : {}),
    });
    if (this.reducedMotion) this.staticFrameSent = true;
    return true;
  }

  private sendReadyIfNeeded() {
    if (this.readySent || !this.visibleAscii || this.startupSteps < 8) return;
    this.readySent = true;
    scope.postMessage({
      type: "ready",
      startupSteps: this.startupSteps,
      renderer: this.renderer,
      vendor: this.vendor,
      unmaskedRenderer: this.unmaskedRenderer,
      unmaskedVendor: this.unmaskedVendor,
    });
  }

  private finishCompletedGrid() {
    if (!this.completedGrid || this.inFlight) return false;
    const pending = this.completedGrid;
    this.completedGrid = undefined;
    const sent = this.finishGrid(pending);
    if (sent && this.visibleAscii) this.initialFrameSent = true;
    if (sent) this.sendReadyIfNeeded();
    return sent;
  }

  private prepareGrid(now: number): boolean {
    if (
      this.pendingGrid ||
      this.completedGrid ||
      !this.visible ||
      (this.inFlight && !this.pboReadbackEnabled)
    )
      return false;
    const computeStarted = performance.now();
    const grid = this.grid;
    const gridWidth = grid.width;
    const gridHeight = grid.height;
    const elapsed = this.textPointer.last
      ? Math.min((now - this.textPointer.last) / 1000, 5)
      : 1 / 30;
    this.textPointer.last = now;
    const decay = Math.exp(-elapsed / 0.6);
    for (let index = 0; index < grid.area; index++) {
      // The worker retains a compact top-down trail aligned with the helper's grid.
      // The buffer is created lazily so the worker's fluid path stays allocation-free.
      this.trail[index] *= decay;
      if (this.trail[index] < 0.006) this.trail[index] = 0;
    }
    if (this.textPointer.active)
      this.textPointer.points.push([this.textPointer.x, this.textPointer.y]);
    for (const [pointX, pointY] of this.textPointer.points) {
      const radius = 85;
      const left = Math.max(0, Math.floor((pointX - radius) / this.charWidth));
      const right = Math.min(
        gridWidth - 1,
        Math.ceil((pointX + radius) / this.charWidth),
      );
      const top = Math.max(0, Math.floor((pointY - radius) / this.lineHeight));
      const bottom = Math.min(
        gridHeight - 1,
        Math.ceil((pointY + radius) / this.lineHeight),
      );
      for (let y = top; y <= bottom; y++) {
        for (let x = left; x <= right; x++) {
          const dx = (x + 0.5) * this.charWidth - pointX;
          const dy = (y + 0.5) * this.lineHeight - pointY;
          const influence =
            0.95 * Math.exp(-(dx * dx + dy * dy) / (2 * 28 * 28));
          if (influence > 0.006) {
            const slot = y * gridWidth + x;
            this.trail[slot] = Math.max(this.trail[slot], influence);
          }
        }
      }
    }
    this.textPointer.points.length = 0;
    const sampleStarted = this.perfEnabled ? performance.now() : 0;
    this.draw(
      this.sampleAscii,
      this.sampleTarget,
      {
        resolution: [Math.max(this.width, 1), Math.max(this.height, 1)],
        coverage: this.coverage,
        zoom: this.state.zoom,
      },
      { density: this.ink.read },
    );
    this.context.bindFramebuffer(
      this.context.FRAMEBUFFER,
      this.sampleTarget.framebuffer,
    );
    const pending: PendingGrid = {
      now,
      computeStarted,
      sampleStarted,
      sampleWidth: gridWidth,
      sampleHeight: gridHeight,
      issuedAt: performance.now(),
    };
    const status = this.queueReadback(pending);
    if (status === "pending") return false;
    pending.issuedAt = performance.now();
    if (this.inFlight) {
      this.completedGrid = pending;
      return false;
    }
    return this.finishGrid(pending);
  }

  private trail: Float32Array;
  private charWidth: number;
  private lineHeight: number = ASCII_REFERENCE.lineHeight;
  private sampleTarget: RenderTarget;

  private frame(now: number) {
    this.animationFrame = undefined;
    if (this.disposed) return;
    this.pollGpuTimers();
    this.finishCompletedGrid();
    const completedReadback = this.pollReadback();
    if (completedReadback) {
      const sent = this.finishGrid(completedReadback);
      if (sent && this.visibleAscii) this.initialFrameSent = true;
      this.sendReadyIfNeeded();
    }
    this.stepsThisFrame = 0;
    this.sampleGpuThisFrame = this.perfEnabled && this.frameCounter++ % 8 === 0;
    this.gpuQueryStartedThisFrame = false;
    if (!this.visible) {
      return;
    }
    if (!this.prepared) {
      // Hidden prewarm and cold visible workers use the original two-step
      // preparation. Visible cold density is trimmed after each step so this
      // preparation never paints accelerated smoke above the viewport.
      const bootstrapSteps = 2;
      for (let index = 0; index < bootstrapSteps; index++) {
        this.step(1 / 60, 1 / 60);
        if (this.activated && !this.reducedMotion) this.trimInkAboveViewport();
        this.startupSteps++;
        this.stepsThisFrame++;
      }
      this.draw(
        this.display,
        null,
        {
          time: this.simulationTime,
          resolution: [Math.max(this.width, 1), Math.max(this.height, 1)],
          zoom: this.state.zoom,
          asciiOn: 1,
        },
        { density: this.ink.read },
      );
      if (this.perfEnabled) this.stepCountTimings.add(this.stepsThisFrame);
      if (
        this.startupSteps >= 8 &&
        (!this.reducedMotion || this.startupSteps >= 72)
      ) {
        const sent = this.prepareGrid(now);
        if (sent && this.visibleAscii) this.initialFrameSent = true;
        if (sent) this.sendReadyIfNeeded();
      }
      if (
        this.startupSteps >= 72 &&
        (!this.reducedMotion || this.initialFrameSent)
      ) {
        this.prepared = true;
        this.lastFrame = 0;
        this.accumulator = 0;
        scope.postMessage({
          type: "prepared",
          startupSteps: this.startupSteps,
        });
      } else {
        if (this.reducedMotion && this.staticFrameSent && this.visibleAscii)
          return;
        this.animationFrame = requestFrame((next) => this.frame(next));
        return;
      }
    }
    const elapsed = this.lastFrame
      ? Math.min((now - this.lastFrame) / 1000, 0.1)
      : 0;
    this.lastFrame = now;
    if (!this.state.paused) {
      this.accumulator += elapsed;
      while (this.accumulator >= 1 / 60) {
        this.step((1 / 60) * this.state.speed * 1.45, 1 / 60);
        this.accumulator -= 1 / 60;
        this.stepsThisFrame++;
      }
    } else this.accumulator = 0;
    if (this.perfEnabled) this.stepCountTimings.add(this.stepsThisFrame);
    this.draw(
      this.display,
      null,
      {
        time: this.simulationTime,
        resolution: [Math.max(this.width, 1), Math.max(this.height, 1)],
        zoom: this.state.zoom,
        asciiOn: 1,
      },
      { density: this.ink.read },
    );
    if (
      this.pboReadbackEnabled ||
      !this.inFlight ||
      now - this.lastGridSample > 250
    ) {
      const sent = this.prepareGrid(now);
      if (sent) this.sendReadyIfNeeded();
    }
    if (this.reducedMotion && this.staticFrameSent) return;
    this.animationFrame = requestFrame((next) => this.frame(next));
  }

  dispose() {
    this.disposed = true;
    if (this.animationFrame) cancelFrame(this.animationFrame);
    this.releaseReadbackRing();
    for (const query of this.pendingGpuQueries) this.context.deleteQuery(query);
    this.pendingGpuQueries.length = 0;
    for (const target of this.renderTargets) {
      this.context.deleteFramebuffer(target.framebuffer);
      this.context.deleteTexture(target.texture);
    }
    for (const shader of this.programs)
      this.context.deleteProgram(shader.program);
    this.context.deleteBuffer(this.buffer);
    this.context.deleteVertexArray(this.vertexArray);
  }
}

let runtime: SmokeRuntime | undefined;

scope.onmessage = (event) => {
  try {
    const message = event.data;
    if (message.type === "init") {
      runtime?.dispose();
      runtime = new SmokeRuntime(message);
      runtime.start();
      return;
    }
    if (!runtime) return;
    if (message.type === "resize") runtime.resize(message);
    else if (message.type === "activate")
      runtime.activate(message.mountEpoch, message.reducedMotion);
    else if (message.type === "pointer") runtime.pointer(message);
    else if (message.type === "visibility") runtime.visibility(message.visible);
    else if (message.type === "motion") runtime.motion(message.reducedMotion);
    else if (message.type === "ack") runtime.acknowledge(message.sequence);
    else if (message.type === "request-full") runtime.requestFull();
  } catch (error) {
    scope.postMessage({
      type: "error",
      message: error instanceof Error ? error.message : "Smoke worker failed.",
    });
  }
};
