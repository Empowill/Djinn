import { useEffect, useRef } from "react";

import { t } from "./i18n";
import {
  ASCII_CHARS,
  ASCII_LAYER_COUNT,
  ASCII_REFERENCE,
  ASCII_TONES,
  computeAsciiBrightness,
  type AsciiGridFrame,
} from "./wish-ascii-grid";
import {
  computeWishSmokeCameraScale,
  computeWishSmokeLayout,
  interpolateWishSmokeCameraScale,
  type WishSmokeLayout,
} from "./wish-smoke-layout";

type ShaderProgram = {
  program: WebGLProgram;
  uniforms: Record<string, WebGLUniformLocation | null>;
};

type RenderTarget = {
  texture: WebGLTexture;
  framebuffer: WebGLFramebuffer;
  width: number;
  height: number;
};

type DoubleTarget = RenderTarget & {
  read: RenderTarget;
  write: RenderTarget;
  swap: () => void;
};

type SmokeState = {
  density: number;
  speed: number;
  detail: number;
  zoom: number;
  paused: boolean;
};

type SmokePerformanceCleanup = () => void;

// Opt-in browser instrumentation for measuring the real renderer without adding work to ordinary sessions. Open the
// creation surface with `?djinnPerf=1`; attributes are published on the creation and smoke roots for CUA inspection.
function mountSmokePerformanceProbe(
  canvas: HTMLCanvasElement,
): SmokePerformanceCleanup {
  if (new URLSearchParams(window.location.search).get("djinnPerf") !== "1")
    return () => undefined;

  const smoke = canvas.parentElement;
  const creation = canvas.closest<HTMLElement>(".wish-creation");
  const targets = [creation, smoke, canvas].filter(
    (target): target is HTMLElement => target !== null,
  );
  for (const target of targets) target.dataset.perfEnabled = "true";
  const frameIntervals: Array<{ value: number; at: number }> = [];
  const pendingInputs: number[] = [];
  const inputLatencies: number[] = [];
  const longTasks: Array<{ start: number; duration: number }> = [];
  const windowMs = 10_000;
  let animationFrame = 0;
  let lastFrame: number | undefined;
  let lastPublish = 0;
  let stopped = false;

  const quantile = (values: number[], percentile: number) => {
    if (!values.length) return undefined;
    const ordered = values.slice().sort((left, right) => left - right);
    return ordered[
      Math.min(
        ordered.length - 1,
        Math.floor((ordered.length - 1) * percentile),
      )
    ];
  };
  const rounded = (value: number | undefined) =>
    value === undefined ? undefined : Math.round(value * 100) / 100;
  const setAttribute = (name: string, value: number | undefined) => {
    for (const target of targets) {
      if (value === undefined || !Number.isFinite(value))
        target.removeAttribute(name);
      else target.setAttribute(name, String(rounded(value)));
    }
  };
  const prune = (now: number) => {
    // Intervals are recorded at their ending timestamp in a parallel queue to keep the published window bounded.
    while (frameIntervals.length && frameIntervals[0].at < now - windowMs)
      frameIntervals.shift();
    while (
      longTasks.length &&
      longTasks[0].start + longTasks[0].duration < now - windowMs
    )
      longTasks.shift();
    while (inputLatencies.length > 600) inputLatencies.shift();
  };
  const publish = (now: number) => {
    if (now - lastPublish < 250) return;
    lastPublish = now;
    const intervals = frameIntervals.map((sample) => sample.value);
    const median = quantile(intervals, 0.5);
    const p95 = quantile(intervals, 0.95);
    const p99 = quantile(intervals, 0.99);
    const longTaskDuration = longTasks.reduce(
      (total, task) => total + task.duration,
      0,
    );
    for (const target of targets) {
      target.dataset.perfEnabled = "true";
      target.dataset.perfWindowMs = String(windowMs);
      target.dataset.perfFramesOver25ms = String(
        intervals.filter((interval) => interval >= 25).length,
      );
      target.dataset.perfLongtasks = String(longTasks.length);
      target.dataset.perfLongtaskMs = String(rounded(longTaskDuration) ?? 0);
    }
    setAttribute("data-perf-raf-median", median);
    setAttribute("data-perf-raf-p95", p95);
    setAttribute("data-perf-raf-p99", p99);
    setAttribute("data-perf-fps-median", median ? 1000 / median : undefined);
    setAttribute("data-perf-input-latency-p95", quantile(inputLatencies, 0.95));
  };
  const frame = (now: number) => {
    if (stopped) return;
    if (document.hidden) {
      animationFrame = 0;
      lastFrame = undefined;
      pendingInputs.length = 0;
      return;
    }
    if (lastFrame !== undefined && now > lastFrame) {
      frameIntervals.push({ value: now - lastFrame, at: now });
      for (const started of pendingInputs.splice(0))
        inputLatencies.push(now - started);
    }
    lastFrame = now;
    prune(now);
    publish(now);
    animationFrame = window.requestAnimationFrame(frame);
  };
  const resume = () => {
    if (stopped || document.hidden || animationFrame) return;
    lastFrame = undefined;
    animationFrame = window.requestAnimationFrame(frame);
  };
  const onVisibilityChange = () => {
    if (document.hidden) {
      if (animationFrame) window.cancelAnimationFrame(animationFrame);
      animationFrame = 0;
      lastFrame = undefined;
      pendingInputs.length = 0;
    } else resume();
  };
  const onInput = () => {
    if (!document.hidden) pendingInputs.push(performance.now());
  };
  for (const type of ["keydown", "input", "pointerdown"])
    document.addEventListener(type, onInput, true);
  document.addEventListener("visibilitychange", onVisibilityChange);
  let observer: PerformanceObserver | undefined;
  try {
    observer = new PerformanceObserver((list) => {
      for (const entry of list.getEntries())
        longTasks.push({ start: entry.startTime, duration: entry.duration });
    });
    observer.observe({ type: "longtask", buffered: true });
  } catch {
    observer = undefined;
  }
  resume();

  return () => {
    stopped = true;
    if (animationFrame) window.cancelAnimationFrame(animationFrame);
    observer?.disconnect();
    document.removeEventListener("visibilitychange", onVisibilityChange);
    for (const type of ["keydown", "input", "pointerdown"])
      document.removeEventListener(type, onInput, true);
    for (const target of targets) {
      for (const name of [
        "perfEnabled",
        "perfWindowMs",
        "perfRafMedian",
        "perfRafP95",
        "perfRafP99",
        "perfFpsMedian",
        "perfInputLatencyP95",
        "perfFramesOver25ms",
        "perfLongtasks",
        "perfLongtaskMs",
      ])
        delete target.dataset[name];
    }
  };
}

const FONT_FAMILY =
  'ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace';
const ASCII_PALETTE = [
  "#7d8790",
  "#919ca3",
  "#a7b1b3",
  "#c1c8c5",
  "#e3e6df",
  "#fffdf2",
  "#fff0d4",
  "#cad0d5",
];
const CAMERA_BOTTOM_CUTOFF_ZOOM10 = 0.158724;

function usesNativeMacSmokeLayout(): boolean {
  return document.documentElement.classList.contains("native-mac");
}

function createAsciiLayers(asciiOutput: HTMLElement) {
  asciiOutput.replaceChildren();
  const layers = ASCII_PALETTE.map((color, index) => {
    const layer = document.createElement("pre");
    layer.setAttribute("aria-hidden", "true");
    const opacity = index < ASCII_TONES.length ? ASCII_TONES[index] : 0.95;
    layer.style.opacity = String(opacity);
    layer.style.color = color;
    asciiOutput.append(layer);
    return layer;
  });
  return layers;
}

function applyAsciiLayout(
  asciiOutput: HTMLElement,
  layers: readonly HTMLElement[],
  layout: WishSmokeLayout,
) {
  asciiOutput.style.fontSize = `${layout.fontSize}px`;
  asciiOutput.style.lineHeight = `${layout.lineHeight}px`;
  const textWidth = layout.gridWidth * layout.charWidth;
  const textHeight = layout.gridHeight * layout.lineHeight;
  for (const layer of layers) {
    // Keep the committed text surface anchored to the right and bottom while
    // the surrounding creation layout changes. The next full frame swaps in
    // new dimensions atomically, so a sidebar resize never drags old glyphs.
    layer.style.left = "auto";
    layer.style.right = "0";
    layer.style.top = "auto";
    layer.style.bottom = "0";
    layer.style.width = `${textWidth}px`;
    layer.style.height = `${textHeight}px`;
    layer.style.transform = "none";
  }
}
// Adapted from the user's supplied local generated reference at /Users/clementfauvelle/Documents/Codex/2026-10-09/
// referenced-chatgpt-conversation-this-is-an/outputs/smoke/{smoke.js,style.css,index.html}; no third-party dependency.

type AsciiGrid = {
  texture: WebGLTexture;
  framebuffer: WebGLFramebuffer;
  width: number;
  height: number;
  charWidth: number;
  lineHeight: number;
  pixels: Uint8Array;
  trail: Float32Array;
  trailActive: boolean;
  coverage: [number, number];
};

function mountSmoke(
  canvas: HTMLCanvasElement,
  asciiOutput: HTMLElement,
): () => void {
  const container = canvas.parentElement;
  if (!container) return () => undefined;

  let gl: WebGL2RenderingContext | null = null;
  try {
    gl = canvas.getContext("webgl2", {
      alpha: false,
      antialias: false,
      powerPreference: "high-performance",
    });
    if (!gl || !gl.getExtension("EXT_color_buffer_float")) {
      throw new Error("WebGL 2 is required for the smoke study.");
    }
  } catch (error) {
    canvas.dataset.error =
      error instanceof Error ? error.message : "WebGL is unavailable.";
    return () => undefined;
  }

  const context = gl;
  if (canvas.dataset.perfEnabled === "true") {
    const renderer = String(context.getParameter(context.RENDERER));
    const vendor = String(context.getParameter(context.VENDOR));
    for (const target of [canvas, canvas.parentElement]) {
      if (!target) continue;
      target.dataset.perfRendererWebgl = renderer;
      target.dataset.perfVendorWebgl = vendor;
    }
  }
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const state: SmokeState = {
    density: 1.23,
    speed: 0.04,
    detail: 10,
    zoom: 10,
    paused: reducedMotion.matches,
  };
  canvas.dataset.zoom = String(state.zoom);

  const simulationWidth = window.innerWidth < 600 ? 192 : 320;
  const simulationHeight = simulationWidth * 2;
  const densityWidth = simulationWidth * 4;
  const densityHeight = simulationHeight * 4;
  const seed = Math.random() * 500;
  const vertex = `#version 300 es
    in vec2 position; out vec2 uv;
    void main(){uv=position*.5+.5;gl_Position=vec4(position,0.,1.);}`;
  const common = `#version 300 es
    precision highp float; in vec2 uv; out vec4 color;
    uniform vec2 texel; uniform float dt; uniform float time;
    uniform sampler2D velocity; uniform sampler2D density;
    uniform sampler2D pressure; uniform sampler2D curl;
    uniform sampler2D divergence; uniform sampler2D source;`;
  const programs: ShaderProgram[] = [];
  const renderTargets: RenderTarget[] = [];

  const makeProgram = (
    fragment: string,
    vertexSource = vertex,
  ): ShaderProgram => {
    const shader = (type: number, source: string): WebGLShader => {
      const compiled = context.createShader(type);
      if (!compiled) throw new Error("Unable to create a smoke shader.");
      context.shaderSource(compiled, source);
      context.compileShader(compiled);
      if (!context.getShaderParameter(compiled, context.COMPILE_STATUS)) {
        const info =
          context.getShaderInfoLog(compiled) ||
          "Smoke shader compilation failed.";
        context.deleteShader(compiled);
        throw new Error(info);
      }
      return compiled;
    };
    const vertexShader = shader(context.VERTEX_SHADER, vertexSource);
    const fragmentShader = shader(context.FRAGMENT_SHADER, common + fragment);
    const program = context.createProgram();
    if (!program) throw new Error("Unable to create a smoke program.");
    context.attachShader(program, vertexShader);
    context.attachShader(program, fragmentShader);
    context.linkProgram(program);
    context.deleteShader(vertexShader);
    context.deleteShader(fragmentShader);
    if (!context.getProgramParameter(program, context.LINK_STATUS)) {
      const info =
        context.getProgramInfoLog(program) || "Smoke program linking failed.";
      context.deleteProgram(program);
      throw new Error(info);
    }
    const uniforms: Record<string, WebGLUniformLocation | null> = {};
    const count = context.getProgramParameter(
      program,
      context.ACTIVE_UNIFORMS,
    ) as number;
    for (let index = 0; index < count; index++) {
      const info = context.getActiveUniform(program, index);
      if (info)
        uniforms[info.name] = context.getUniformLocation(program, info.name);
    }
    const result = { program, uniforms };
    programs.push(result);
    return result;
  };

  const createTarget = (
    width = simulationWidth,
    height = simulationHeight,
  ): RenderTarget => {
    const texture = context.createTexture();
    const framebuffer = context.createFramebuffer();
    if (!texture || !framebuffer)
      throw new Error("Unable to create the smoke render target.");
    context.bindTexture(context.TEXTURE_2D, texture);
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_MIN_FILTER,
      context.LINEAR,
    );
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_MAG_FILTER,
      context.LINEAR,
    );
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_WRAP_S,
      context.CLAMP_TO_EDGE,
    );
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_WRAP_T,
      context.CLAMP_TO_EDGE,
    );
    context.texImage2D(
      context.TEXTURE_2D,
      0,
      context.RGBA16F,
      width,
      height,
      0,
      context.RGBA,
      context.HALF_FLOAT,
      null,
    );
    context.bindFramebuffer(context.FRAMEBUFFER, framebuffer);
    context.framebufferTexture2D(
      context.FRAMEBUFFER,
      context.COLOR_ATTACHMENT0,
      context.TEXTURE_2D,
      texture,
      0,
    );
    if (
      context.checkFramebufferStatus(context.FRAMEBUFFER) !==
      context.FRAMEBUFFER_COMPLETE
    ) {
      throw new Error("Smoke rendering is unavailable on this device.");
    }
    context.viewport(0, 0, width, height);
    context.clearColor(0, 0, 0, 0);
    context.clear(context.COLOR_BUFFER_BIT);
    const target = { texture, framebuffer, width, height };
    renderTargets.push(target);
    return target;
  };

  const createDoubleTarget = (
    width = simulationWidth,
    height = simulationHeight,
  ): DoubleTarget => {
    const target = {
      read: createTarget(width, height),
      write: createTarget(width, height),
      swap() {
        [target.read, target.write] = [target.write, target.read];
      },
    } as DoubleTarget;
    return target;
  };

  const buffer = context.createBuffer();
  const vertexArray = context.createVertexArray();
  if (!buffer || !vertexArray)
    throw new Error("Unable to create the smoke geometry.");
  context.bindBuffer(context.ARRAY_BUFFER, buffer);
  context.bufferData(
    context.ARRAY_BUFFER,
    new Float32Array([-1, -1, 1, -1, -1, 1, -1, 1, 1, -1, 1, 1]),
    context.STATIC_DRAW,
  );
  context.bindVertexArray(vertexArray);
  context.enableVertexAttribArray(0);
  context.vertexAttribPointer(0, 2, context.FLOAT, false, 0, 0);

  let disposed = false;
  let simulationTime = 0;
  let windTime = seed;
  let animationFrame = 0;
  let resizeFrame = 0;
  let prepared = false;
  let startupSteps = 0;
  let visibleAscii = false;
  let lastFrame = 0;
  let accumulator = 0;
  let grid: AsciiGrid | null = null;
  let lastAscii = -Infinity;
  let cameraScale = computeWishSmokeCameraScale(1, 1);
  let cameraStartScale = cameraScale;
  let cameraTargetScale = cameraScale;
  let cameraTransitionStarted = 0;
  const tones = ASCII_TONES;
  const layers = createAsciiLayers(asciiOutput);
  const measureCanvas = document.createElement("canvas");
  const measure = measureCanvas.getContext("2d");
  if (!measure) throw new Error("Unable to measure the smoke text.");
  measure.fontKerning = "none";
  const textPointer = {
    x: -1000,
    y: -1000,
    active: false,
    points: [] as Array<[number, number]>,
    last: 0,
  };

  const setCameraSurface = (width: number, height: number) => {
    const now = performance.now();
    cameraScale = interpolateWishSmokeCameraScale(
      cameraStartScale,
      cameraTargetScale,
      now - cameraTransitionStarted,
    );
    cameraStartScale = cameraScale;
    cameraTargetScale = computeWishSmokeCameraScale(width, height);
    cameraTransitionStarted = now;
    if (state.paused) cameraScale = cameraTargetScale;
  };
  const updateCamera = (now: number) => {
    cameraScale = state.paused
      ? cameraTargetScale
      : interpolateWishSmokeCameraScale(
          cameraStartScale,
          cameraTargetScale,
          now - cameraTransitionStarted,
        );
  };

  const velocity = createDoubleTarget();
  const ink = createDoubleTarget(densityWidth, densityHeight);
  const pressure = createDoubleTarget();
  const divergence = createTarget();
  const curl = createTarget();

  const draw = (
    shader: ShaderProgram,
    destination: RenderTarget | null,
    values: Record<string, number | [number, number]> = {},
    textures: Record<string, WebGLTexture | RenderTarget> = {},
  ) => {
    context.useProgram(shader.program);
    context.bindVertexArray(vertexArray);
    context.bindFramebuffer(
      context.FRAMEBUFFER,
      destination && "framebuffer" in destination
        ? destination.framebuffer
        : null,
    );
    context.viewport(
      0,
      0,
      destination && "width" in destination ? destination.width : canvas.width,
      destination && "height" in destination
        ? destination.height
        : canvas.height,
    );
    if (shader.uniforms.texel)
      context.uniform2f(
        shader.uniforms.texel,
        1 / simulationWidth,
        1 / simulationHeight,
      );
    for (const [name, value] of Object.entries(values)) {
      const location = shader.uniforms[name];
      if (!location) continue;
      if (Array.isArray(value)) context.uniform2fv(location, value);
      else context.uniform1f(location, value);
    }
    let unit = 0;
    for (const [name, value] of Object.entries(textures)) {
      const location = shader.uniforms[name];
      if (!location) continue;
      context.activeTexture(context.TEXTURE0 + unit);
      context.bindTexture(
        context.TEXTURE_2D,
        "texture" in value ? value.texture : value,
      );
      context.uniform1i(location, unit++);
    }
    context.drawArrays(context.TRIANGLES, 0, 6);
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
    v.x+=(wind+shear)*smoothstep(.06,.55,p.y)*(.00390625/texel.x);
    return v;}`;
  const advect = makeProgram(
    `${hoistedFlow}uniform float dissipation;
    void main(){vec2 v=flow(uv);color=sampleLinear(source,uv-dt*v*texel)*exp(-dissipation*dt);}`,
    windVertex,
  );
  const advectSmoke = makeProgram(
    `${hoistedFlow}uniform float dissipation;
    void main(){vec2 p=uv-dt*flow(uv)*texel;float d=sampleLinear(source,p).x;
    vec2 spread=texel*vec2(.75,1.5);
    float surrounding=(sampleLinear(source,p+vec2(spread.x,0)).x+sampleLinear(source,p-vec2(spread.x,0)).x+
      sampleLinear(source,p+vec2(0,spread.y)).x+sampleLinear(source,p-vec2(0,spread.y)).x)*.25;
    d=mix(d,surrounding,1.-exp(-14.*dt));color=vec4(d*exp(-dissipation*dt),0,0,1);}`,
    windVertex,
  );
  const forces = makeProgram(`${wandering}
    void main(){vec2 v=texture(velocity,uv).xy;float d=texture(density,uv).x;
    float height=smoothstep(.09,.65,uv.y);float breeze=sin(uv.y*10.-time*.64)*7.+sin(uv.y*23.+time*.37)*4.;
    float scale=.00390625/texel.x;v.x+=dt*(breeze*height+sin(time*1.8)*.8)*(.12+min(d,1.))*scale;
    v.y+=dt*d*60.*scale;vec2 delta=(uv-emitter())*vec2(1.,1.8);float jet=exp(-dot(delta,delta)/.00009);
    v=mix(v,vec2(2.*sin(time*1.5)+sin(time*3.1),62.+6.*sin(time*.8))*scale,min(.95,jet*.5));
    if(uv.x<texel.x||uv.x>1.-texel.x)v.x=0.;if(uv.y<texel.y)v.y=0.;color=vec4(v,0,1);}`);
  const inject =
    makeProgram(`${wandering}uniform float amount;void main(){float d=texture(density,uv).x;
    vec2 delta=(uv-emitter())*vec2(.85,1.8);float jet=exp(-dot(delta,delta)/.000075);
    d+=jet*dt*amount*9.2*(.78+.22*sin(time*2.4));d*=1.-smoothstep(.86,1.,uv.y)*dt*1.4;color=vec4(d,0,0,1);}`);
  const curlProgram =
    makeProgram(`void main(){float l=texture(velocity,uv-vec2(texel.x,0)).y;
    float r=texture(velocity,uv+vec2(texel.x,0)).y;float b=texture(velocity,uv-vec2(0,texel.y)).x;
    float t=texture(velocity,uv+vec2(0,texel.y)).x;color=vec4(.5*(r-l-t+b),0,0,1);}`);
  const confine =
    makeProgram(`void main(){float l=abs(texture(curl,uv-vec2(texel.x,0)).x);
    float r=abs(texture(curl,uv+vec2(texel.x,0)).x);float b=abs(texture(curl,uv-vec2(0,texel.y)).x);
    float t=abs(texture(curl,uv+vec2(0,texel.y)).x);float c=texture(curl,uv).x;vec2 f=.5*vec2(t-b,r-l);
    f/=length(f)+.0001;f*=10.*c;f.y*=-1.;color=vec4(clamp(texture(velocity,uv).xy+dt*f,vec2(-160.),vec2(160.)),0,1);}`);
  const divergenceProgram =
    makeProgram(`void main(){float l=texture(velocity,uv-vec2(texel.x,0)).x;
    float r=texture(velocity,uv+vec2(texel.x,0)).x;float b=texture(velocity,uv-vec2(0,texel.y)).y;
    float t=texture(velocity,uv+vec2(0,texel.y)).y;color=vec4(.5*(r-l+t-b),0,0,1);}`);
  const pressureProgram =
    makeProgram(`void main(){float l=texture(pressure,uv-vec2(texel.x,0)).x;
    float r=texture(pressure,uv+vec2(texel.x,0)).x;float b=texture(pressure,uv-vec2(0,texel.y)).x;
    float t=texture(pressure,uv+vec2(0,texel.y)).x;color=vec4((l+r+b+t-texture(divergence,uv).x)*.25,0,0,1);}`);
  const gradient =
    makeProgram(`void main(){float l=texture(pressure,uv-vec2(texel.x,0)).x;
    float r=texture(pressure,uv+vec2(texel.x,0)).x;float b=texture(pressure,uv-vec2(0,texel.y)).x;
    float t=texture(pressure,uv+vec2(0,texel.y)).x;color=vec4(texture(velocity,uv).xy-.5*vec2(r-l,t-b),0,1);}`);
  const smokeField = `${bilinear}
    uniform vec2 resolution;uniform float zoom;uniform float cameraScale;
    float hash(vec2 p){return fract(sin(dot(p,vec2(127.1,311.7)))*43758.5453);}
    vec2 domain(vec2 p){
      vec2 q=vec2((.5-p.x)*cameraScale+.5,(p.y-.038)/1.16);
      vec2 focus=vec2(.565,.18);return focus+(q-focus)/zoom;}
    float smoke(vec2 p){vec2 q=domain(p);if(q.x<0.||q.x>1.||q.y<0.||q.y>1.)return 0.;
      float d=sampleLinear(density,q).x;vec2 cell=1./vec2(textureSize(density,0));
      float dx=(sampleLinear(density,q+vec2(cell.x,0)).x-sampleLinear(density,q-vec2(cell.x,0)).x)*texel.x/cell.x;
      float light=.80+.20*tanh(dx*4.);return clamp((1.-exp(-d*6.5))*light,0.,1.);}`;
  const display = makeProgram(`${smokeField}
    uniform float asciiOn;
    void main(){
      vec2 pixel=uv*resolution;float raw=smoke(uv);
      float value=asciiOn>.5?raw*.025:raw;
      float grain=(hash(pixel+floor(time*20.))-.5)*.007;
      float vignette=1.-.24*length((uv-.5)*vec2(1.,.7));
      vec3 bg=vec3(.018,.022,.032);
      vec3 tint=mix(vec3(.56,.60,.63),vec3(.93,.90,.84),smoothstep(.08,.75,raw));
      color=vec4(bg+tint*value*vignette+vec3(grain),1.);
    }`);
  const sampleAscii = makeProgram(`${smokeField}uniform vec2 coverage;
    void main(){float d=smoke(uv*coverage+vec2(1.-coverage.x,0.));color=vec4(d,d,d,1.);}`);
  const trimDensity = makeProgram(`uniform float cutoff;uniform float edge;
    void main(){float inkValue=texture(density,uv).x;
    float above=smoothstep(cutoff-edge,cutoff+edge,uv.y);
    color=vec4(inkValue*(1.-above),0,0,1.);}`);

  const resizeAscii = (
    layout: ReturnType<typeof computeWishSmokeLayout>,
    surfaceWidth: number,
    surfaceHeight: number,
  ) => {
    if (grid) {
      context.deleteFramebuffer(grid.framebuffer);
      context.deleteTexture(grid.texture);
    }
    const width = layout.gridWidth;
    const height = layout.gridHeight;
    asciiOutput.style.fontSize = `${layout.fontSize}px`;
    asciiOutput.style.lineHeight = `${layout.lineHeight}px`;
    const texture = context.createTexture();
    const framebuffer = context.createFramebuffer();
    if (!texture || !framebuffer)
      throw new Error("Unable to create the smoke text buffer.");
    context.bindTexture(context.TEXTURE_2D, texture);
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_MIN_FILTER,
      context.NEAREST,
    );
    context.texParameteri(
      context.TEXTURE_2D,
      context.TEXTURE_MAG_FILTER,
      context.NEAREST,
    );
    context.texImage2D(
      context.TEXTURE_2D,
      0,
      context.RGBA8,
      width,
      height,
      0,
      context.RGBA,
      context.UNSIGNED_BYTE,
      null,
    );
    context.bindFramebuffer(context.FRAMEBUFFER, framebuffer);
    context.framebufferTexture2D(
      context.FRAMEBUFFER,
      context.COLOR_ATTACHMENT0,
      context.TEXTURE_2D,
      texture,
      0,
    );
    grid = {
      framebuffer,
      texture,
      width,
      height,
      charWidth: layout.charWidth,
      lineHeight: layout.lineHeight,
      pixels: new Uint8Array(width * height * 4),
      trail: new Float32Array(width * height),
      trailActive: false,
      coverage: [...layout.coverage] as [number, number],
    };
    applyAsciiLayout(asciiOutput, layers, layout);
    setCameraSurface(surfaceWidth, surfaceHeight);
    lastAscii = -Infinity;
  };

  const trimInkAboveViewport = () => {
    const edge = 1 / Math.max(1, densityHeight);
    draw(
      trimDensity,
      ink.write,
      {
        cutoff: CAMERA_BOTTOM_CUTOFF_ZOOM10 - edge,
        edge,
      },
      { density: ink.read },
    );
    ink.swap();
  };

  const resize = () => {
    const rect = container.getBoundingClientRect();
    measure.font = `${ASCII_REFERENCE.fontSize}px ${FONT_FAMILY}`;
    const baseCharWidth = Math.max(1, measure.measureText("M").width);
    const layout = computeWishSmokeLayout(
      Math.max(rect.width, 1),
      Math.max(rect.height, 1),
      baseCharWidth,
      window.devicePixelRatio || 1,
      usesNativeMacSmokeLayout(),
    );
    canvas.width = layout.canvasWidth;
    canvas.height = layout.canvasHeight;
    resizeAscii(layout, Math.max(rect.width, 1), Math.max(rect.height, 1));
  };

  const renderAscii = (now: number) => {
    if (!grid || now - lastAscii < 1000 / 30) return;
    const elapsed = textPointer.last
      ? Math.min((now - textPointer.last) / 1000, 5)
      : 1 / 30;
    textPointer.last = now;
    const decay = Math.exp(-elapsed / 0.6);
    const hadTrail = grid.trailActive;
    grid.trailActive = false;
    for (let index = 0; index < grid.trail.length; index++) {
      grid.trail[index] *= decay;
      if (grid.trail[index] < 0.006) grid.trail[index] = 0;
      else grid.trailActive = true;
    }
    if (textPointer.active)
      textPointer.points.push([textPointer.x, textPointer.y]);
    for (const [pointX, pointY] of textPointer.points) {
      const radius = 85;
      const left = Math.max(0, Math.floor((pointX - radius) / grid.charWidth));
      const right = Math.min(
        grid.width - 1,
        Math.ceil((pointX + radius) / grid.charWidth),
      );
      const top = Math.max(0, Math.floor((pointY - radius) / grid.lineHeight));
      const bottom = Math.min(
        grid.height - 1,
        Math.ceil((pointY + radius) / grid.lineHeight),
      );
      for (let y = top; y <= bottom; y++) {
        for (let x = left; x <= right; x++) {
          const dx = (x + 0.5) * grid.charWidth - pointX;
          const dy = (y + 0.5) * grid.lineHeight - pointY;
          const influence =
            0.95 * Math.exp(-(dx * dx + dy * dy) / (2 * 28 * 28));
          const slot = y * grid.width + x;
          if (influence > 0.006) {
            grid.trail[slot] = Math.max(grid.trail[slot], influence);
            grid.trailActive = true;
          }
        }
      }
    }
    textPointer.points.length = 0;
    if (
      state.paused &&
      visibleAscii &&
      Number.isFinite(lastAscii) &&
      !hadTrail &&
      !grid.trailActive
    )
      return;
    lastAscii = now;
    const rect = container.getBoundingClientRect();
    draw(
      sampleAscii,
      grid,
      {
        resolution: [Math.max(rect.width, 1), Math.max(rect.height, 1)],
        coverage: grid.coverage,
        zoom: state.zoom,
        cameraScale,
      },
      { density: ink.read },
    );
    context.bindFramebuffer(context.FRAMEBUFFER, grid.framebuffer);
    context.readPixels(
      0,
      0,
      grid.width,
      grid.height,
      context.RGBA,
      context.UNSIGNED_BYTE,
      grid.pixels,
    );
    if (!visibleAscii) {
      for (let index = 0; index < grid.pixels.length; index += 4) {
        if (grid.pixels[index] > 10) {
          visibleAscii = true;
          if (prepared) canvas.dataset.ready = "true";
          break;
        }
      }
    }
    const maskedTrail = grid.trail;
    const rows = layers.map(() => [] as string[]);
    for (let y = 0; y < grid.height; y++) {
      const row = layers.map(() => "");
      for (let x = 0; x < grid.width; x++) {
        const level =
          grid.pixels[((grid.height - 1 - y) * grid.width + x) * 4] / 255;
        const noise = (((x * 37 + y * 91) % 17) / 17) * 0.065 - 0.0325;
        let index = Math.max(
          0,
          Math.min(
            ASCII_CHARS.length - 1,
            Math.floor((Math.pow(level, 0.52) + noise) * ASCII_CHARS.length),
          ),
        );
        const brightness = computeAsciiBrightness(level);
        let lower = 0;
        while (lower < tones.length - 1 && tones[lower + 1] < brightness)
          lower++;
        const upper = Math.min(lower + 1, tones.length - 1);
        let cellHash =
          Math.imul(x + 1, 374761393) ^ Math.imul(y + 1, 668265263);
        cellHash = Math.imul(cellHash ^ (cellHash >>> 13), 1274126177);
        const blend =
          upper === lower
            ? 0
            : Math.max(
                0,
                Math.min(
                  1,
                  (brightness - tones[lower]) / (tones[upper] - tones[lower]),
                ),
              );
        let tone = (cellHash >>> 0) / 4294967296 < blend ? upper : lower;
        const influence = maskedTrail[y * grid.width + x];
        if (index && ((x * 73 + y * 37) % 101) / 101 < influence * 0.95) {
          const shift = (Math.floor(now / 160) + x * 17 + y * 13) % 2 ? 2 : -2;
          index = Math.max(1, Math.min(ASCII_CHARS.length - 1, index + shift));
          tone = tones.length + (influence > 0.5 ? 0 : 1);
        }
        for (let layer = 0; layer < layers.length; layer++)
          row[layer] +=
            layer === tone && brightness > 0.035 ? ASCII_CHARS[index] : " ";
      }
      for (let layer = 0; layer < layers.length; layer++)
        rows[layer].push(row[layer]);
    }
    for (let index = 0; index < layers.length; index++) {
      const text = rows[index].join("\n");
      if (layers[index].textContent !== text) layers[index].textContent = text;
    }
  };

  resize();

  const step = (dt: number, wallDt = dt) => {
    simulationTime += dt;
    windTime += wallDt;
    const values = { dt, time: simulationTime + seed, windTime };
    draw(
      advect,
      velocity.write,
      { ...values, dissipation: 0.13 },
      { velocity: velocity.read, source: velocity.read },
    );
    velocity.swap();
    draw(forces, velocity.write, values, {
      velocity: velocity.read,
      density: ink.read,
    });
    velocity.swap();
    draw(curlProgram, curl, values, { velocity: velocity.read });
    draw(confine, velocity.write, values, { velocity: velocity.read, curl });
    velocity.swap();
    draw(divergenceProgram, divergence, values, { velocity: velocity.read });
    for (let index = 0; index < 16; index++) {
      draw(pressureProgram, pressure.write, values, {
        pressure: pressure.read,
        divergence,
      });
      pressure.swap();
    }
    draw(gradient, velocity.write, values, {
      velocity: velocity.read,
      pressure: pressure.read,
    });
    velocity.swap();
    draw(
      advectSmoke,
      ink.write,
      { ...values, dissipation: 0.055 },
      { velocity: velocity.read, source: ink.read },
    );
    ink.swap();
    draw(
      inject,
      ink.write,
      { ...values, amount: state.density },
      { density: ink.read },
    );
    ink.swap();
  };

  const frame = (now: number) => {
    if (disposed) return;
    if (!surfaceVisible()) {
      animationFrame = 0;
      return;
    }
    if (!prepared) {
      // Keep the bounded preparation cadence even for reduced motion.  Those
      // steps happen before the static frame is exposed, and reduced motion
      // deliberately skips the viewport trim so the final image is complete.
      const bootstrapSteps = 2;
      for (let index = 0; index < bootstrapSteps; index++) {
        step(1 / 60, 1 / 60);
        if (!state.paused) trimInkAboveViewport();
        startupSteps++;
      }
      render();
      if (startupSteps >= 72 && (!state.paused || visibleAscii)) {
        prepared = true;
        lastFrame = 0;
        accumulator = 0;
        lastAscii = -Infinity;
        if (visibleAscii) canvas.dataset.ready = "true";
        canvas.dataset.startupSteps = String(startupSteps);
      } else {
        animationFrame = window.requestAnimationFrame(frame);
        return;
      }
    }
    const elapsed = lastFrame ? Math.min((now - lastFrame) / 1000, 0.1) : 0;
    lastFrame = now;
    if (!state.paused && surfaceVisible()) {
      accumulator += elapsed;
      while (accumulator >= 1 / 60) {
        step((1 / 60) * state.speed * 1.45, 1 / 60);
        accumulator -= 1 / 60;
      }
    } else {
      accumulator = 0;
    }
    render();
    animationFrame = window.requestAnimationFrame(frame);
  };

  const render = () => {
    const now = performance.now();
    updateCamera(now);
    if (surfaceVisible() && (!state.paused || startupSteps >= 72))
      renderAscii(now);
    const rect = container.getBoundingClientRect();
    draw(
      display,
      null,
      {
        time: simulationTime,
        resolution: [Math.max(rect.width, 1), Math.max(rect.height, 1)],
        zoom: state.zoom,
        cameraScale,
        asciiOn: 1,
      },
      { density: ink.read },
    );
    canvas.dataset.visible = "true";
    canvas.dataset.renderWidth = String(Math.round(rect.width));
  };

  const onPointerMove = (event: PointerEvent) => {
    const wasActive = textPointer.active;
    const rect = container.getBoundingClientRect();
    const pointX = event.clientX - rect.left;
    const pointY = event.clientY - rect.top;
    const target = event.target;
    textPointer.active =
      !(target instanceof Element) ||
      !target.closest("button,input,.settings-panel");
    if (!textPointer.active) return;
    const steps = wasActive
      ? Math.min(
          32,
          Math.max(
            1,
            Math.ceil(
              Math.hypot(pointX - textPointer.x, pointY - textPointer.y) / 16,
            ),
          ),
        )
      : 1;
    for (let index = 1; index <= steps; index++)
      textPointer.points.push(
        wasActive
          ? [
              textPointer.x + ((pointX - textPointer.x) * index) / steps,
              textPointer.y + ((pointY - textPointer.y) * index) / steps,
            ]
          : [pointX, pointY],
      );
    textPointer.points = textPointer.points.slice(-128);
    textPointer.x = pointX;
    textPointer.y = pointY;
  };
  const onPointerLeave = () => {
    textPointer.active = false;
  };
  const surfaceForm = canvas.closest<HTMLFormElement>("form");
  const surfaceVisible = () => !document.hidden && !surfaceForm?.hidden;
  const onVisibilityChange = () => {
    lastFrame = 0;
    accumulator = 0;
    if (!surfaceVisible()) {
      if (animationFrame) cancelAnimationFrame(animationFrame);
      animationFrame = 0;
    } else if (!animationFrame) {
      animationFrame = requestAnimationFrame(frame);
    }
  };
  const onMotionPreferenceChange = () => {
    state.paused = reducedMotion.matches;
  };
  const onWindowResize = () => {
    if (resizeFrame) cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => {
      resizeFrame = 0;
      resize();
    });
  };
  const observer = new ResizeObserver(onWindowResize);
  observer.observe(container);
  const visibilityObserver =
    surfaceForm && typeof MutationObserver !== "undefined"
      ? new MutationObserver(onVisibilityChange)
      : undefined;
  if (visibilityObserver && surfaceForm)
    visibilityObserver.observe(surfaceForm, {
      attributes: true,
      attributeFilter: ["hidden"],
    });
  const interactionTarget =
    canvas.closest<HTMLElement>(".wish-creation") ?? container;
  interactionTarget.addEventListener("pointermove", onPointerMove);
  interactionTarget.addEventListener("pointerleave", onPointerLeave);
  document.addEventListener("visibilitychange", onVisibilityChange);
  reducedMotion.addEventListener?.("change", onMotionPreferenceChange);
  window.addEventListener("blur", onPointerLeave);
  canvas.dataset.ready = "false";
  canvas.dataset.visible = "false";
  animationFrame = requestAnimationFrame(frame);

  return () => {
    disposed = true;
    cancelAnimationFrame(animationFrame);
    if (resizeFrame) cancelAnimationFrame(resizeFrame);
    observer.disconnect();
    visibilityObserver?.disconnect();
    interactionTarget.removeEventListener("pointermove", onPointerMove);
    interactionTarget.removeEventListener("pointerleave", onPointerLeave);
    document.removeEventListener("visibilitychange", onVisibilityChange);
    reducedMotion.removeEventListener?.("change", onMotionPreferenceChange);
    window.removeEventListener("blur", onPointerLeave);
    canvas.dataset.visible = "false";
    if (grid) {
      context.deleteFramebuffer(grid.framebuffer);
      context.deleteTexture(grid.texture);
    }
    asciiOutput.replaceChildren();
    for (const target of renderTargets) {
      context.deleteFramebuffer(target.framebuffer);
      context.deleteTexture(target.texture);
    }
    for (const shader of programs) context.deleteProgram(shader.program);
    context.deleteBuffer(buffer);
    context.deleteVertexArray(vertexArray);
    for (const target of [canvas, canvas.parentElement]) {
      if (!target) continue;
      for (const name of ["perfRendererWebgl", "perfVendorWebgl"])
        delete target.dataset[name];
    }
  };
}

type SmokeWorkerFrameMessage = {
  type: "frame";
  frame: AsciiGridFrame;
  layoutGeneration: number;
  computeMs: number;
  gridMs: number;
  metrics?: SmokeWorkerMetrics;
};

type SmokeWorkerTimingSummary = {
  p50: number;
  p95: number;
  p99: number;
  count: number;
};

type SmokeWorkerMetrics = {
  windowMs: number;
  cpuStep?: SmokeWorkerTimingSummary;
  stepCount?: SmokeWorkerTimingSummary;
  sample?: SmokeWorkerTimingSummary;
  grid?: SmokeWorkerTimingSummary;
  gpu?: SmokeWorkerTimingSummary;
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
  scalarFormat: "r16f" | "rgba16f";
  velocityFormat: "rg16f" | "rgba16f";
  readbackMode: "pbo" | "direct";
};

function isSmokeWorkerFrame(
  message: unknown,
): message is SmokeWorkerFrameMessage {
  return (
    typeof message === "object" &&
    message !== null &&
    (message as { type?: unknown }).type === "frame" &&
    typeof (message as { frame?: unknown }).frame === "object" &&
    typeof (message as { layoutGeneration?: unknown }).layoutGeneration ===
      "number"
  );
}

type SmokePrewarmCache = {
  worker: Worker;
  workerCanvas: HTMLCanvasElement;
  host: HTMLDivElement;
  onMessage: (event: MessageEvent<unknown>) => void;
  onError: (event: ErrorEvent) => void;
  onDocumentVisibility: () => void;
  ready: boolean;
  failed: boolean;
};

let smokePrewarmCache: SmokePrewarmCache | undefined;
let smokePrewarmModuleDisposed = false;

function createSmokePrewarmHost() {
  const host = document.createElement("div");
  host.setAttribute("aria-hidden", "true");
  host.dataset.wishSmokePrewarm = "true";
  host.style.cssText =
    "position:fixed;left:-10000px;top:-10000px;width:1px;height:1px;overflow:hidden;opacity:0;pointer-events:none;";
  return host;
}

export function startWishSmokePrewarm() {
  if (
    smokePrewarmModuleDisposed ||
    smokePrewarmCache ||
    typeof window === "undefined" ||
    typeof Worker === "undefined" ||
    typeof HTMLCanvasElement === "undefined"
  )
    return;
  if (!document.body) {
    queueMicrotask(() => {
      if (!smokePrewarmModuleDisposed) startWishSmokePrewarm();
    });
    return;
  }
  let worker: Worker;
  try {
    worker = new Worker(new URL("./wish-smoke-worker.ts", import.meta.url), {
      type: "module",
    });
  } catch {
    return;
  }
  const host = createSmokePrewarmHost();
  const workerCanvas = document.createElement("canvas");
  workerCanvas.className = "wish-smoke-worker-canvas";
  workerCanvas.width = 256;
  workerCanvas.height = 512;
  workerCanvas.style.cssText =
    "display:block;position:relative;width:1px;height:1px;visibility:hidden;";
  host.append(workerCanvas);
  document.body.append(host);
  let cache: SmokePrewarmCache;
  const disposeCache = () => {
    if (cache.failed) return;
    cache.failed = true;
    if (smokePrewarmCache === cache) smokePrewarmCache = undefined;
    worker.removeEventListener("message", onMessage);
    worker.removeEventListener("error", onError);
    document.removeEventListener("visibilitychange", onDocumentVisibility);
    host.remove();
    worker.terminate();
  };
  const onMessage = (event: MessageEvent<unknown>) => {
    const message = event.data as { type?: string };
    if (message?.type === "error") {
      disposeCache();
      return;
    }
    if (isSmokeWorkerFrame(message)) {
      worker.postMessage({ type: "ack", sequence: message.frame.sequence });
      return;
    }
    if (message?.type === "prepared") {
      cache.ready = true;
      worker.postMessage({ type: "visibility", visible: false });
    }
  };
  const onError = (event: ErrorEvent) => {
    event.preventDefault();
    disposeCache();
  };
  const onDocumentVisibility = () => {
    if (cache.failed || smokePrewarmCache !== cache) return;
    worker.postMessage({
      type: "visibility",
      visible: !document.hidden && !cache.ready,
    });
  };
  cache = {
    worker,
    workerCanvas,
    host,
    onMessage,
    onError,
    onDocumentVisibility,
    ready: false,
    failed: false,
  };
  smokePrewarmCache = cache;
  worker.addEventListener("message", onMessage);
  worker.addEventListener("error", onError);
  document.addEventListener("visibilitychange", onDocumentVisibility);
  let offscreen: OffscreenCanvas;
  try {
    if (typeof workerCanvas.transferControlToOffscreen !== "function")
      throw new Error("Offscreen smoke rendering is unavailable.");
    offscreen = workerCanvas.transferControlToOffscreen();
  } catch {
    disposeCache();
    return;
  }
  worker.postMessage(
    {
      type: "init",
      canvas: offscreen,
      viewportWidth: 1024,
      width: 1,
      height: 1,
      canvasWidth: 256,
      canvasHeight: 512,
      pixelRatio: 1,
      charWidth: ASCII_REFERENCE.fontSize,
      lineHeight: ASCII_REFERENCE.lineHeight,
      gridWidth: 64,
      gridHeight: 32,
      coverage: [1, 1],
      layoutGeneration: 0,
      reducedMotion: false,
      perfEnabled:
        new URLSearchParams(window.location.search).get("djinnPerf") === "1",
      mountEpoch: performance.timeOrigin + performance.now(),
      prewarm: true,
    },
    [offscreen],
  );
}

function takeSmokePrewarm(): SmokePrewarmCache | undefined {
  const cache = smokePrewarmCache;
  if (!cache) return undefined;
  smokePrewarmCache = undefined;
  if (cache.failed) {
    cache.host.remove();
    cache.worker.terminate();
    return undefined;
  }
  cache.worker.removeEventListener("message", cache.onMessage);
  cache.worker.removeEventListener("error", cache.onError);
  document.removeEventListener("visibilitychange", cache.onDocumentVisibility);
  cache.host.remove();
  cache.workerCanvas.style.cssText = "";
  return cache;
}

function returnSmokePrewarm(cache: SmokePrewarmCache) {
  if (smokePrewarmModuleDisposed || cache.failed || smokePrewarmCache)
    return false;
  cache.workerCanvas.style.cssText =
    "display:block;position:relative;width:1px;height:1px;visibility:hidden;";
  cache.host.append(cache.workerCanvas);
  document.body?.append(cache.host);
  cache.worker.addEventListener("message", cache.onMessage);
  cache.worker.addEventListener("error", cache.onError);
  document.addEventListener("visibilitychange", cache.onDocumentVisibility);
  smokePrewarmCache = cache;
  cache.worker.postMessage({
    type: "visibility",
    visible: !document.hidden && !cache.ready,
  });
  return true;
}

export function disposeWishSmokePrewarm() {
  smokePrewarmModuleDisposed = true;
  const cache = smokePrewarmCache;
  if (!cache) return;
  cache.failed = true;
  smokePrewarmCache = undefined;
  cache.worker.removeEventListener("message", cache.onMessage);
  cache.worker.removeEventListener("error", cache.onError);
  document.removeEventListener("visibilitychange", cache.onDocumentVisibility);
  cache.host.remove();
  cache.worker.terminate();
}

const smokeHotModule = (
  import.meta as ImportMeta & {
    hot?: { dispose: (callback: () => void) => void };
  }
).hot;
smokeHotModule?.dispose(disposeWishSmokePrewarm);

function mountSmokeWorker(
  canvas: HTMLCanvasElement,
  asciiOutput: HTMLElement,
  mountEpoch = performance.timeOrigin + performance.now(),
): () => void {
  if (typeof Worker === "undefined")
    throw new Error("Smoke workers are unavailable.");
  const container = canvas.parentElement;
  const prewarm = takeSmokePrewarm();
  if (
    !container ||
    (!prewarm && typeof canvas.transferControlToOffscreen !== "function")
  ) {
    if (prewarm) returnSmokePrewarm(prewarm);
    throw new Error("Offscreen smoke rendering is unavailable.");
  }

  const layers = createAsciiLayers(asciiOutput);
  const measureCanvas = document.createElement("canvas");
  const measure = measureCanvas.getContext("2d");
  if (!measure) throw new Error("Unable to measure the smoke text.");
  measure.font = `${ASCII_REFERENCE.fontSize}px ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace`;
  measure.fontKerning = "none";
  const baseCharWidth = Math.max(1, measure.measureText("M").width);
  const layout = () => {
    const rect = container.getBoundingClientRect();
    const width = Math.max(1, rect.width);
    const height = Math.max(1, rect.height);
    const computed = computeWishSmokeLayout(
      width,
      height,
      baseCharWidth,
      window.devicePixelRatio || 1,
      usesNativeMacSmokeLayout(),
    );
    return {
      width,
      height,
      ...computed,
      coverage: [...computed.coverage] as [number, number],
    };
  };

  const worker =
    prewarm?.worker ??
    new Worker(new URL("./wish-smoke-worker.ts", import.meta.url), {
      type: "module",
    });
  const workerCanvas =
    prewarm?.workerCanvas ?? document.createElement("canvas");
  workerCanvas.className = "wish-smoke-worker-canvas";
  workerCanvas.setAttribute("role", "img");
  workerCanvas.setAttribute(
    "aria-label",
    canvas.getAttribute("aria-label") || "",
  );
  workerCanvas.dataset.zoom = String(ASCII_REFERENCE.zoom);
  workerCanvas.dataset.ready = "false";
  workerCanvas.dataset.visible = "false";
  if (canvas.dataset.perfEnabled === "true")
    workerCanvas.dataset.perfEnabled = "true";
  container.insertBefore(workerCanvas, canvas);
  const perfEnabled = canvas.dataset.perfEnabled === "true";
  const perfTargets = Array.from(
    new Set(
      [
        canvas,
        workerCanvas,
        container,
        canvas.closest<HTMLElement>(".wish-creation"),
      ].filter((target): target is HTMLElement => target !== null),
    ),
  );
  const domTimings: Array<{ value: number; at: number }> = [];
  const changedIntervals: Array<{ value: number; at: number }> = [];
  const changedFrames: number[] = [];
  const maxDomSamples = 600;
  const domWindowMs = 10_000;
  const changedWindowMs = 10_000;
  let lastChangedFrameAt: number | undefined;
  const setPerfAttribute = (
    name: string,
    value: number | string | undefined,
  ) => {
    if (!perfEnabled) return;
    for (const target of perfTargets) {
      if (value === undefined || value === "") target.removeAttribute(name);
      else target.setAttribute(name, String(value));
    }
  };
  const roundedMetric = (value: number) =>
    String(Math.round(value * 100) / 100);
  const publishTimingSummary = (
    name: string,
    summary: SmokeWorkerTimingSummary | undefined,
  ) => {
    if (!summary) return;
    setPerfAttribute(`data-perf-${name}-p50`, roundedMetric(summary.p50));
    setPerfAttribute(`data-perf-${name}-p95`, roundedMetric(summary.p95));
    setPerfAttribute(`data-perf-${name}-p99`, roundedMetric(summary.p99));
    setPerfAttribute(`data-perf-${name}-count`, summary.count);
  };
  const summarizeDomTimings = (): SmokeWorkerTimingSummary | undefined => {
    const now = performance.now();
    while (domTimings.length && domTimings[0].at < now - domWindowMs)
      domTimings.shift();
    if (!domTimings.length) return undefined;
    const ordered = domTimings
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
  };
  const recordChangedFrame = (at: number) => {
    if (lastChangedFrameAt !== undefined) {
      changedIntervals.push({ value: at - lastChangedFrameAt, at });
      if (changedIntervals.length > maxDomSamples) changedIntervals.shift();
    }
    lastChangedFrameAt = at;
    changedFrames.push(at);
    while (changedFrames.length && changedFrames[0] < at - changedWindowMs)
      changedFrames.shift();
    while (
      changedIntervals.length &&
      changedIntervals[0].at < at - changedWindowMs
    )
      changedIntervals.shift();
  };
  const summarizeChangedIntervals = ():
    SmokeWorkerTimingSummary | undefined => {
    if (!changedIntervals.length) return undefined;
    const ordered = changedIntervals
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
      count: changedIntervals.length,
    };
  };
  const publishLayoutMetrics = (next: {
    width: number;
    height: number;
    gridWidth: number;
    gridHeight: number;
    canvasWidth: number;
    canvasHeight: number;
    backgroundPixelRatio: number;
  }) => {
    if (!perfEnabled) return;
    setPerfAttribute("data-perf-canvas-css-width", roundedMetric(next.width));
    setPerfAttribute("data-perf-canvas-css-height", roundedMetric(next.height));
    setPerfAttribute("data-perf-canvas-pixel-width", next.canvasWidth);
    setPerfAttribute("data-perf-canvas-pixel-height", next.canvasHeight);
    setPerfAttribute("data-perf-grid-width", next.gridWidth);
    setPerfAttribute("data-perf-grid-height", next.gridHeight);
    setPerfAttribute("data-perf-grid-cells", next.gridWidth * next.gridHeight);
    setPerfAttribute(
      "data-perf-pixel-ratio",
      roundedMetric(next.backgroundPixelRatio),
    );
  };
  let offscreen: OffscreenCanvas | undefined;
  if (!prewarm) {
    try {
      offscreen = workerCanvas.transferControlToOffscreen();
    } catch (error) {
      worker.terminate();
      workerCanvas.remove();
      throw error;
    }
  }

  let stopped = false;
  let fallbackCleanup: (() => void) | undefined;
  let workerDisposed = false;
  let pointerFrameHandle = 0;
  let resizeFrameHandle = 0;
  let ready = false;
  let prewarmActivated = !prewarm;
  let prewarmSessionReady = !prewarm;
  let activatePrewarm = () => undefined;
  let appliedSequence = 0;
  const textChunkWidth = 64;
  let rowNodes: Array<Array<Array<Text>>> = Array.from(
    { length: ASCII_LAYER_COUNT },
    () => [],
  );
  let gridWidth = 0;
  let gridHeight = 0;
  let activeLayout: ReturnType<typeof layout>;
  let pendingLayout:
    | { layout: ReturnType<typeof layout>; generation: number }
    | undefined;
  let layoutGeneration = prewarm ? 1 : 0;
  let committedLayoutGeneration = layoutGeneration;
  let pendingPointer = {
    type: "pointer" as const,
    x: 0,
    y: 0,
    active: false,
  };

  const installLayer = (layer: number, text: string) => {
    const lines = text.split("\n");
    const layerRows: Array<Array<Text>> = [];
    const fragment = document.createDocumentFragment();
    for (let row = 0; row < gridHeight; row++) {
      const line = (lines[row] || "")
        .padEnd(gridWidth, " ")
        .slice(0, gridWidth);
      const chunks: Text[] = [];
      for (let start = 0; start < gridWidth; start += textChunkWidth) {
        const end = Math.min(gridWidth, start + textChunkWidth);
        const node = document.createTextNode(line.slice(start, end));
        chunks.push(node);
        fragment.append(node);
      }
      layerRows.push(chunks);
      if (row < gridHeight - 1) fragment.append(document.createTextNode("\n"));
    }
    rowNodes[layer] = layerRows;
    layers[layer].replaceChildren(fragment);
  };
  const resetRows = (nextWidth: number, nextHeight: number) => {
    gridWidth = nextWidth;
    gridHeight = nextHeight;
    for (let layer = 0; layer < layers.length; layer++) installLayer(layer, "");
  };
  const sameLayout = (
    left: ReturnType<typeof layout>,
    right: ReturnType<typeof layout>,
  ) =>
    left.width === right.width &&
    left.height === right.height &&
    left.fontSize === right.fontSize &&
    left.charWidth === right.charWidth &&
    left.lineHeight === right.lineHeight &&
    left.gridWidth === right.gridWidth &&
    left.gridHeight === right.gridHeight &&
    left.coverage[0] === right.coverage[0] &&
    left.coverage[1] === right.coverage[1] &&
    left.offsetY === right.offsetY &&
    left.canvasWidth === right.canvasWidth &&
    left.canvasHeight === right.canvasHeight &&
    left.backgroundPixelRatio === right.backgroundPixelRatio;
  const commitLayout = (
    next: ReturnType<typeof layout>,
    generation: number,
  ) => {
    activeLayout = next;
    committedLayoutGeneration = generation;
    appliedSequence = 0;
    resetRows(next.gridWidth, next.gridHeight);
    applyAsciiLayout(asciiOutput, layers, next);
    publishLayoutMetrics(next);
  };
  const publishMetric = (name: string, value: number) => {
    if (!perfEnabled) return;
    const metric = roundedMetric(value);
    const kebabName = name.replace(
      /[A-Z]/g,
      (letter) => `-${letter.toLowerCase()}`,
    );
    setPerfAttribute(`data-perf-worker${kebabName}`, metric);
  };
  const publishWorkerMetrics = (metrics: SmokeWorkerMetrics | undefined) => {
    if (!perfEnabled || !metrics) return;
    setPerfAttribute("data-perf-timing-window-ms", metrics.windowMs);
    setPerfAttribute(
      "data-perf-gpu-timer",
      metrics.gpuTimer ? "supported" : "unavailable",
    );
    setPerfAttribute("data-perf-gl-texture-bytes", metrics.textureBytes);
    setPerfAttribute("data-perf-renderer", metrics.renderer);
    setPerfAttribute("data-perf-vendor", metrics.vendor);
    setPerfAttribute("data-perf-gl-scalar-format", metrics.scalarFormat);
    setPerfAttribute("data-perf-gl-velocity-format", metrics.velocityFormat);
    setPerfAttribute("data-perf-gl-readback-mode", metrics.readbackMode);
    setPerfAttribute("data-perf-renderer-unmasked", metrics.unmaskedRenderer);
    setPerfAttribute("data-perf-vendor-unmasked", metrics.unmaskedVendor);
    setPerfAttribute("data-perf-canvas-pixel-width", metrics.canvasWidth);
    setPerfAttribute("data-perf-canvas-pixel-height", metrics.canvasHeight);
    setPerfAttribute("data-perf-grid-width", metrics.gridWidth);
    setPerfAttribute("data-perf-grid-height", metrics.gridHeight);
    setPerfAttribute("data-perf-grid-cells", metrics.gridCells);
    setPerfAttribute(
      "data-perf-pixel-ratio",
      roundedMetric(metrics.pixelRatio),
    );
    publishTimingSummary("worker-cpu-step", metrics.cpuStep);
    publishTimingSummary("worker-steps", metrics.stepCount);
    publishTimingSummary("worker-sample", metrics.sample);
    publishTimingSummary("worker-grid", metrics.grid);
    publishTimingSummary("worker-gpu", metrics.gpu);
  };
  const publishReadyIfVisible = () => {
    if (!ready || canvas.dataset.visible !== "true") return;
    canvas.dataset.ready = "true";
    workerCanvas.dataset.ready = "true";
  };
  const applyFrame = (message: SmokeWorkerFrameMessage) => {
    if (prewarm && !prewarmSessionReady) {
      worker.postMessage({ type: "ack", sequence: message.frame.sequence });
      return;
    }
    const started = perfEnabled ? performance.now() : 0;
    let mutatedTextNodes = 0;
    const frame = message.frame;
    const expectedLayout = pendingLayout?.layout ?? activeLayout;
    const expectedGeneration =
      pendingLayout?.generation ?? committedLayoutGeneration;
    if (message.layoutGeneration !== expectedGeneration) {
      // A frame from a superseded resize must never replace the committed
      // text. Acknowledge it only to release the worker's backpressure slot.
      worker.postMessage({ type: "ack", sequence: frame.sequence });
      return;
    }
    if (pendingLayout) {
      if (
        !frame.full ||
        !frame.fullStrings ||
        frame.width !== pendingLayout.layout.gridWidth ||
        frame.height !== pendingLayout.layout.gridHeight
      ) {
        worker.postMessage({ type: "request-full" });
        worker.postMessage({ type: "ack", sequence: frame.sequence });
        return;
      }
      commitLayout(pendingLayout.layout, pendingLayout.generation);
      pendingLayout = undefined;
    } else if (
      frame.width !== expectedLayout.gridWidth ||
      frame.height !== expectedLayout.gridHeight
    ) {
      worker.postMessage({ type: "request-full" });
      worker.postMessage({ type: "ack", sequence: frame.sequence });
      return;
    }
    if (frame.full && frame.fullStrings) {
      for (let index = 0; index < layers.length; index++) {
        installLayer(index, frame.fullStrings[index] || "");
      }
      mutatedTextNodes =
        layers.length * Math.ceil(gridWidth / textChunkWidth) * gridHeight;
    } else {
      if (frame.baseSequence !== appliedSequence) {
        worker.postMessage({ type: "request-full" });
        worker.postMessage({ type: "ack", sequence: appliedSequence });
        return;
      }
      for (const patch of frame.patches) {
        const layerRows = rowNodes[patch.layer];
        const chunks = layerRows?.[patch.row];
        if (!chunks) continue;
        let sourceOffset = 0;
        let cursor = patch.start;
        while (cursor < patch.end) {
          const chunkStart =
            Math.floor(cursor / textChunkWidth) * textChunkWidth;
          const chunkEnd = Math.min(gridWidth, chunkStart + textChunkWidth);
          const amount = Math.min(patch.end - cursor, chunkEnd - cursor);
          const chunk = chunks[Math.floor(cursor / textChunkWidth)];
          if (chunk) {
            const current = chunk.data.padEnd(chunkEnd - chunkStart, " ");
            const localStart = cursor - chunkStart;
            const next =
              current.slice(0, localStart) +
              patch.text.slice(sourceOffset, sourceOffset + amount) +
              current.slice(localStart + amount);
            if (next !== chunk.data) {
              chunk.data = next;
              mutatedTextNodes++;
            }
          }
          cursor += amount;
          sourceOffset += amount;
        }
      }
    }
    appliedSequence = frame.sequence;
    worker.postMessage({ type: "ack", sequence: appliedSequence });
    canvas.dataset.visible = "true";
    workerCanvas.dataset.visible = "true";
    publishReadyIfVisible();
    if (perfEnabled) {
      const appliedAt = performance.now();
      const domElapsed = appliedAt - started;
      publishMetric("DomMs", domElapsed);
      publishMetric("ComputeMs", message.computeMs);
      publishMetric("GridMs", message.gridMs);
      domTimings.push({ value: domElapsed, at: appliedAt });
      if (domTimings.length > maxDomSamples) domTimings.shift();
      setPerfAttribute("data-perf-main-dom-patches", frame.patches.length);
      setPerfAttribute("data-perf-main-dom-text-mutations", mutatedTextNodes);
      recordChangedFrame(appliedAt);
      setPerfAttribute(
        "data-perf-main-dom-changed-count",
        changedFrames.length,
      );
      publishTimingSummary("main-dom", summarizeDomTimings());
      publishTimingSummary(
        "main-dom-changed-interval",
        summarizeChangedIntervals(),
      );
      publishWorkerMetrics(message.metrics);
    }
  };
  const startFallback = (error?: unknown) => {
    if (stopped || fallbackCleanup) return;
    workerDisposed = true;
    worker.terminate();
    resizeObserver.disconnect();
    interactionTarget.removeEventListener("pointermove", onPointerMove);
    interactionTarget.removeEventListener("pointerleave", onPointerLeave);
    document.removeEventListener("visibilitychange", onVisibilityChange);
    reducedMotion.removeEventListener("change", onMotionPreferenceChange);
    window.removeEventListener("blur", onPointerLeave);
    if (pointerFrameHandle) cancelAnimationFrame(pointerFrameHandle);
    if (resizeFrameHandle) cancelAnimationFrame(resizeFrameHandle);
    worker.removeEventListener("message", onWorkerMessage);
    worker.removeEventListener("error", onWorkerError);
    workerCanvas.remove();
    asciiOutput.replaceChildren();
    canvas.style.display = "";
    canvas.removeAttribute("aria-hidden");
    canvas.dataset.ready = "false";
    canvas.dataset.visible = "false";
    if (error !== undefined) {
      const message =
        error instanceof Error
          ? error.message
          : typeof error === "object" &&
              error !== null &&
              "message" in error &&
              typeof error.message === "string"
            ? error.message
            : typeof error === "string"
              ? error
              : "Smoke rendering is unavailable.";
      canvas.dataset.workerError = message;
    }
    delete canvas.dataset.error;
    try {
      fallbackCleanup = mountSmoke(canvas, asciiOutput);
    } catch (fallbackError) {
      canvas.dataset.error =
        fallbackError instanceof Error
          ? fallbackError.message
          : "Smoke rendering is unavailable.";
    }
  };
  const onWorkerMessage = (event: MessageEvent<unknown>) => {
    const message = event.data as
      | {
          type?: string;
          startupSteps?: number;
          renderer?: string;
          vendor?: string;
          unmaskedRenderer?: string;
          unmaskedVendor?: string;
        }
      | SmokeWorkerFrameMessage;
    if (message?.type === "activated" && prewarm) {
      prewarmSessionReady = true;
      return;
    }
    if (message?.type === "prepared" && prewarm) {
      // A cold borrowed worker is activated immediately on mount.  The
      // prepared notification can therefore arrive after activation; keep
      // the cache state truthful so returning it can go idle instead of
      // continuing to render forever.
      prewarm.ready = true;
      if (!prewarmActivated) activatePrewarm();
      return;
    }
    if (message?.type === "ready") {
      if (!message.startupSteps) return;
      ready = true;
      publishReadyIfVisible();
      if (message.renderer) {
        canvas.dataset.perfRendererWebgl = message.renderer;
        workerCanvas.dataset.perfRendererWebgl = message.renderer;
      }
      if (message.vendor) {
        canvas.dataset.perfVendorWebgl = message.vendor;
        workerCanvas.dataset.perfVendorWebgl = message.vendor;
      }
      setPerfAttribute("data-perf-renderer", message.renderer);
      setPerfAttribute("data-perf-vendor", message.vendor);
      setPerfAttribute("data-perf-renderer-unmasked", message.unmaskedRenderer);
      setPerfAttribute("data-perf-vendor-unmasked", message.unmaskedVendor);
      return;
    }
    if (message?.type === "error") {
      startFallback(message);
      return;
    }
    if (isSmokeWorkerFrame(message)) {
      applyFrame(message);
    }
  };
  const onWorkerError = (event: ErrorEvent) => {
    event.preventDefault();
    startFallback(event.error || event.message);
  };
  worker.addEventListener("message", onWorkerMessage);
  worker.addEventListener("error", onWorkerError);

  const sendResize = () => {
    const next = layout();
    const baseline = pendingLayout?.layout ?? activeLayout;
    if (sameLayout(next, baseline)) return;
    const generation = ++layoutGeneration;
    pendingLayout = { layout: next, generation };
    worker.postMessage({
      type: "resize",
      viewportWidth: window.innerWidth,
      width: next.width,
      height: next.height,
      canvasWidth: next.canvasWidth,
      canvasHeight: next.canvasHeight,
      pixelRatio: next.backgroundPixelRatio,
      charWidth: next.charWidth,
      lineHeight: next.lineHeight,
      gridWidth: next.gridWidth,
      gridHeight: next.gridHeight,
      coverage: next.coverage,
      layoutGeneration: generation,
    });
  };
  const onResize = () => {
    if (resizeFrameHandle) cancelAnimationFrame(resizeFrameHandle);
    resizeFrameHandle = requestAnimationFrame(() => {
      resizeFrameHandle = 0;
      sendResize();
    });
  };
  const resizeObserver = new ResizeObserver(onResize);
  resizeObserver.observe(container);
  const interactionTarget =
    canvas.closest<HTMLElement>(".wish-creation") ?? container;
  const queuePointer = () => {
    if (pointerFrameHandle || stopped) return;
    pointerFrameHandle = requestAnimationFrame(() => {
      pointerFrameHandle = 0;
      worker.postMessage(pendingPointer);
    });
  };
  const onPointerMove = (event: PointerEvent) => {
    const rect = container.getBoundingClientRect();
    const target = event.target;
    const active =
      !(target instanceof Element) ||
      !target.closest("button,input,textarea,select,.settings-panel");
    pendingPointer = {
      type: "pointer",
      x: event.clientX - rect.left,
      y: event.clientY - rect.top,
      active,
    };
    queuePointer();
  };
  const onPointerLeave = () => {
    pendingPointer = { ...pendingPointer, active: false };
    queuePointer();
  };
  const surfaceForm = canvas.closest<HTMLFormElement>("form");
  const surfaceVisible = () => !document.hidden && !surfaceForm?.hidden;
  const onVisibilityChange = () =>
    worker.postMessage({ type: "visibility", visible: surfaceVisible() });
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const onMotionPreferenceChange = () =>
    worker.postMessage({
      type: "motion",
      reducedMotion: reducedMotion.matches,
    });
  interactionTarget.addEventListener("pointermove", onPointerMove);
  interactionTarget.addEventListener("pointerleave", onPointerLeave);
  document.addEventListener("visibilitychange", onVisibilityChange);
  window.addEventListener("blur", onPointerLeave);
  reducedMotion.addEventListener("change", onMotionPreferenceChange);
  const visibilityObserver =
    surfaceForm && typeof MutationObserver !== "undefined"
      ? new MutationObserver(onVisibilityChange)
      : undefined;
  if (visibilityObserver && surfaceForm)
    visibilityObserver.observe(surfaceForm, {
      attributes: true,
      attributeFilter: ["hidden"],
    });
  const initial = layout();
  commitLayout(initial, layoutGeneration);
  canvas.style.display = "none";
  canvas.setAttribute("aria-hidden", "true");
  canvas.dataset.ready = "false";
  canvas.dataset.visible = "false";
  delete canvas.dataset.workerError;
  canvas.dataset.zoom = String(ASCII_REFERENCE.zoom);
  if (prewarm) {
    activatePrewarm = () => {
      if (prewarmActivated || stopped) return;
      prewarmActivated = true;
      prewarmSessionReady = false;
      worker.postMessage({
        type: "activate",
        mountEpoch,
        reducedMotion: reducedMotion.matches,
      });
      worker.postMessage({
        type: "motion",
        reducedMotion: reducedMotion.matches,
      });
      worker.postMessage({
        type: "resize",
        viewportWidth: window.innerWidth,
        width: initial.width,
        height: initial.height,
        canvasWidth: initial.canvasWidth,
        canvasHeight: initial.canvasHeight,
        pixelRatio: initial.backgroundPixelRatio,
        charWidth: initial.charWidth,
        lineHeight: initial.lineHeight,
        gridWidth: initial.gridWidth,
        gridHeight: initial.gridHeight,
        coverage: initial.coverage,
        layoutGeneration,
      });
      worker.postMessage({ type: "request-full" });
      worker.postMessage({ type: "visibility", visible: surfaceVisible() });
    };
    // Activation must not wait for the hidden prewarm to finish its startup
    // budget.  A cold opening should receive the worker's visible frames
    // immediately; `ready` only controls whether a returned cache may idle.
    activatePrewarm();
  } else if (offscreen) {
    worker.postMessage(
      {
        type: "init",
        canvas: offscreen,
        viewportWidth: window.innerWidth,
        width: initial.width,
        height: initial.height,
        canvasWidth: initial.canvasWidth,
        canvasHeight: initial.canvasHeight,
        pixelRatio: initial.backgroundPixelRatio,
        charWidth: initial.charWidth,
        lineHeight: initial.lineHeight,
        gridWidth: initial.gridWidth,
        gridHeight: initial.gridHeight,
        coverage: initial.coverage,
        layoutGeneration,
        reducedMotion: reducedMotion.matches,
        perfEnabled,
        mountEpoch,
        prewarm: false,
      },
      [offscreen],
    );
  }
  if (!surfaceVisible())
    worker.postMessage({ type: "visibility", visible: false });

  return () => {
    stopped = true;
    resizeObserver.disconnect();
    interactionTarget.removeEventListener("pointermove", onPointerMove);
    interactionTarget.removeEventListener("pointerleave", onPointerLeave);
    document.removeEventListener("visibilitychange", onVisibilityChange);
    reducedMotion.removeEventListener("change", onMotionPreferenceChange);
    visibilityObserver?.disconnect();
    window.removeEventListener("blur", onPointerLeave);
    if (pointerFrameHandle) cancelAnimationFrame(pointerFrameHandle);
    if (resizeFrameHandle) cancelAnimationFrame(resizeFrameHandle);
    worker.removeEventListener("message", onWorkerMessage);
    worker.removeEventListener("error", onWorkerError);
    fallbackCleanup?.();
    if (prewarm && !workerDisposed) {
      if (!returnSmokePrewarm(prewarm)) {
        worker.terminate();
        workerCanvas.remove();
      }
    } else {
      worker.terminate();
      workerCanvas.remove();
    }
    asciiOutput.replaceChildren();
    canvas.style.display = "";
    canvas.removeAttribute("aria-hidden");
    canvas.dataset.visible = "false";
    canvas.dataset.ready = "false";
    for (const name of [
      "perfWorkerDomMs",
      "perfWorkerComputeMs",
      "perfWorkerGridMs",
      "perfRendererWebgl",
      "perfVendorWebgl",
    ])
      delete canvas.dataset[name];
    for (const target of perfTargets)
      for (const name of [
        "data-perf-worker-dom-ms",
        "data-perf-worker-compute-ms",
        "data-perf-worker-grid-ms",
        "data-perf-main-dom-patches",
        "data-perf-main-dom-text-mutations",
        "data-perf-main-dom-changed-count",
        "data-perf-timing-window-ms",
        "data-perf-gpu-timer",
        "data-perf-gl-texture-bytes",
        "data-perf-gl-scalar-format",
        "data-perf-gl-velocity-format",
        "data-perf-gl-readback-mode",
        "data-perf-renderer",
        "data-perf-vendor",
        "data-perf-renderer-unmasked",
        "data-perf-vendor-unmasked",
        "data-perf-canvas-css-width",
        "data-perf-canvas-css-height",
        "data-perf-canvas-pixel-width",
        "data-perf-canvas-pixel-height",
        "data-perf-grid-width",
        "data-perf-grid-height",
        "data-perf-grid-cells",
        "data-perf-pixel-ratio",
        "data-perf-worker-cpu-step-p50",
        "data-perf-worker-cpu-step-p95",
        "data-perf-worker-cpu-step-p99",
        "data-perf-worker-cpu-step-count",
        "data-perf-worker-steps-p50",
        "data-perf-worker-steps-p95",
        "data-perf-worker-steps-p99",
        "data-perf-worker-steps-count",
        "data-perf-worker-sample-p50",
        "data-perf-worker-sample-p95",
        "data-perf-worker-sample-p99",
        "data-perf-worker-sample-count",
        "data-perf-worker-grid-p50",
        "data-perf-worker-grid-p95",
        "data-perf-worker-grid-p99",
        "data-perf-worker-grid-count",
        "data-perf-worker-gpu-p50",
        "data-perf-worker-gpu-p95",
        "data-perf-worker-gpu-p99",
        "data-perf-worker-gpu-count",
        "data-perf-main-dom-p50",
        "data-perf-main-dom-p95",
        "data-perf-main-dom-p99",
        "data-perf-main-dom-count",
        "data-perf-main-dom-changed-interval-p50",
        "data-perf-main-dom-changed-interval-p95",
        "data-perf-main-dom-changed-interval-p99",
        "data-perf-main-dom-changed-interval-count",
      ])
        target.removeAttribute(name);
  };
}

export function WishSmoke() {
  const canvas = useRef<HTMLCanvasElement>(null);
  const asciiOutput = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const canvasElement = canvas.current;
    const asciiElement = asciiOutput.current;
    if (!canvasElement || !asciiElement) return undefined;
    const diagnostics = new URLSearchParams(window.location.search);
    const diagnosticsEnabled = diagnostics.get("djinnPerf") === "1";
    const smokeEnabled =
      !diagnosticsEnabled || diagnostics.get("djinnPerfSmoke") !== "0";
    const mountEpoch = performance.timeOrigin + performance.now();
    const performanceCleanup = mountSmokePerformanceProbe(canvasElement);
    if (diagnosticsEnabled) {
      const mode = smokeEnabled ? "enabled" : "disabled";
      canvasElement.dataset.perfSmokeMode = mode;
      canvasElement.parentElement?.setAttribute("data-perf-smoke-mode", mode);
    }
    let cleanup: (() => void) | undefined;
    if (smokeEnabled) {
      try {
        cleanup = mountSmokeWorker(canvasElement, asciiElement, mountEpoch);
      } catch (error) {
        try {
          cleanup = mountSmoke(canvasElement, asciiElement);
        } catch (fallbackError) {
          canvasElement.dataset.error =
            fallbackError instanceof Error
              ? fallbackError.message
              : error instanceof Error
                ? error.message
                : "smoke";
        }
      }
    }
    return () => {
      cleanup?.();
      performanceCleanup();
      delete canvasElement.dataset.perfSmokeMode;
      canvasElement.parentElement?.removeAttribute("data-perf-smoke-mode");
    };
  }, []);

  return (
    <div className="wish-smoke">
      <canvas
        ref={canvas}
        className="wish-smoke-canvas"
        data-zoom="10"
        role="img"
        aria-label={t("make.smoke_label")}
      />
      <div ref={asciiOutput} className="wish-smoke-ascii" aria-hidden="true" />
      <div className="wish-smoke-fallback" role="status">
        {t("make.smoke_fallback")}
      </div>
    </div>
  );
}
