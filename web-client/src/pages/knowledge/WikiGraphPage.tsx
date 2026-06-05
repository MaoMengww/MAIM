import { useRef, useState, useEffect, useMemo, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Spin, Button, Tooltip, Tag, Empty, Input, Checkbox, Switch, Drawer, Divider, Badge } from 'antd';
import {
  ArrowLeftOutlined, ApartmentOutlined, TableOutlined,
  SearchOutlined, ZoomInOutlined, ZoomOutOutlined, AimOutlined,
  LinkOutlined, FileTextOutlined,
} from '@ant-design/icons';
import ForceGraph2D from 'react-force-graph-2d';
import { kbApi } from '@/services/knowledge';
import { WikiMarkdown } from './WikiMarkdown';
import type { WikiGraphNode, WikiGraphEdge, WikiGraphData, WikiPageRsp } from '@/types/model';

/* ─── Type Constants ─── */

const NODE_TYPES = ['entity', 'concept', 'index', 'summary', 'comparison', 'synthesis'] as const;

const TYPE_META: Record<string, { label: string; color: string; light: string }> = {
  entity:     { label: '实体',     color: '#FF6B6B', light: '#FFE0E0' },
  concept:    { label: '概念',     color: '#4ECDC4', light: '#D4F5F2' },
  index:      { label: '索引',     color: '#FFD93D', light: '#FFF8D4' },
  summary:    { label: '概述',     color: '#6BCB77', light: '#DCF5E0' },
  comparison: { label: '对比',     color: '#FF8C42', light: '#FFE8D4' },
  synthesis:  { label: '综合论述', color: '#A66CFF', light: '#E8DCF5' },
};

/* ─── Visual Helpers ─── */

function nodeSize(n: WikiGraphNode): number {
  const cc = typeof n.citation_count === 'number' && isFinite(n.citation_count) ? Math.max(0, n.citation_count) : 0;
  const base = 12 + Math.sqrt(cc) * 3;
  if (n.group === 'entity') return base + cc * 0.02;
  if (n.group === 'concept') return base + cc * 0.02 * 0.8;
  if (n.group === 'summary' || n.group === 'synthesis') return base + 12;
  return base;
}

function fadeColor(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}

function truncateLabel(s: string, max: number): string {
  if (!s || s.length <= max) return s || '';
  return s.slice(0, max) + '…';
}

/* ─── Node Canvas Renderer ─── */

function drawNode(
  node: WikiGraphNode & { x: number; y: number },
  ctx: CanvasRenderingContext2D,
  globalScale: number,
  nodes: WikiGraphNode[],
  highlighted: boolean,
  pulseTime: number,
) {
  const { x, y, group, citation_count } = node;
  if (!isFinite(x) || !isFinite(y)) return;

  const size = nodeSize(node);
  const meta = TYPE_META[group];
  const color = meta?.color || '#86909c';
  const alpha = highlighted ? 1 : 0.12;
  const pulse = citation_count > 100 ? 1 + Math.sin(pulseTime * 2.094) * 0.03 : 1;

  ctx.save();
  ctx.translate(x, y);
  ctx.scale(pulse, pulse);
  ctx.translate(-x, -y);
  ctx.globalAlpha = alpha;

  const R = size;

  // ── Synthesis: glowing aura + solid core ──
  if (group === 'synthesis') {
    // Outer glow ring
    const grad = ctx.createRadialGradient(x, y, R * 0.6, x, y, R * 1.5);
    grad.addColorStop(0, fadeColor(color, 0.3));
    grad.addColorStop(1, fadeColor(color, 0));
    ctx.fillStyle = grad;
    ctx.beginPath(); ctx.arc(x, y, R * 1.5, 0, Math.PI * 2); ctx.fill();

    // Pulsing ring
    const ringPhase = Math.sin(pulseTime * 3) * 0.5 + 0.5;
    ctx.beginPath(); ctx.arc(x, y, R * 1.1 + ringPhase * 6, 0, Math.PI * 2);
    ctx.strokeStyle = fadeColor(color, 0.25 + ringPhase * 0.3);
    ctx.lineWidth = 2.5 / globalScale;
    ctx.stroke();

    // Core
    const core = ctx.createRadialGradient(x - R * 0.25, y - R * 0.25, 0, x, y, R);
    core.addColorStop(0, '#fff');
    core.addColorStop(0.3, color);
    core.addColorStop(1, fadeColor(color, 0.7));
    ctx.fillStyle = core;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2); ctx.fill();

    // Inner star dots
    for (let i = 0; i < 6; i++) {
      const a = (Math.PI / 3) * i + pulseTime * 0.5;
      const d = R * 0.45;
      ctx.beginPath(); ctx.arc(x + Math.cos(a) * d, y + Math.sin(a) * d, 2.5 / globalScale, 0, Math.PI * 2);
      ctx.fillStyle = '#fff';
      ctx.globalAlpha = alpha * (0.5 + Math.sin(pulseTime * 2 + i) * 0.3);
      ctx.fill();
    }
    ctx.globalAlpha = alpha;

  // ── Comparison: half-half split ──
  } else if (group === 'comparison') {
    ctx.shadowColor = 'rgba(0,0,0,0.2)';
    ctx.shadowBlur = 6 / globalScale;
    ctx.shadowOffsetY = 2 / globalScale;
    // Left half
    ctx.beginPath(); ctx.arc(x, y, R, Math.PI * 0.5, Math.PI * 1.5); ctx.closePath();
    ctx.fillStyle = '#FF6B6B';
    ctx.fill();
    // Right half
    ctx.beginPath(); ctx.arc(x, y, R, -Math.PI * 0.5, Math.PI * 0.5); ctx.closePath();
    ctx.fillStyle = '#4ECDC4';
    ctx.fill();
    ctx.shadowBlur = 0;

  // ── Summary: gradient fill ──
  } else if (group === 'summary') {
    const grad = ctx.createRadialGradient(x - R * 0.3, y - R * 0.3, 0, x, y, R);
    grad.addColorStop(0, '#fff');
    grad.addColorStop(0.4, color);
    grad.addColorStop(1, fadeColor(color, 0.65));
    ctx.fillStyle = grad;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2); ctx.fill();

  // ── Index: double ring ──
  } else if (group === 'index') {
    ctx.fillStyle = color;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2);
    ctx.fill();
    // Inner ring cutout
    ctx.fillStyle = '#fff';
    ctx.beginPath(); ctx.arc(x, y, R * 0.45, 0, Math.PI * 2);
    ctx.fill();
    // Pin dot
    ctx.fillStyle = color;
    ctx.beginPath(); ctx.arc(x, y, R * 0.2, 0, Math.PI * 2);
    ctx.fill();

  // ── Concept: dotted border ──
  } else if (group === 'concept') {
    ctx.fillStyle = color;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2);
    ctx.fill();
    ctx.globalAlpha = alpha * 0.5;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2);
    ctx.strokeStyle = '#222';
    ctx.lineWidth = 2.5 / globalScale;
    ctx.setLineDash([4, 4]);
    ctx.stroke();
    ctx.setLineDash([]);

  // ── Entity: solid circle ──
  } else {
    ctx.shadowColor = 'rgba(0,0,0,0.2)';
    ctx.shadowBlur = 6 / globalScale;
    ctx.shadowOffsetY = 2 / globalScale;
    const grad = ctx.createRadialGradient(x - R * 0.2, y - R * 0.2, 0, x, y, R);
    grad.addColorStop(0, fadeColor(color, 1));
    grad.addColorStop(0.7, color);
    grad.addColorStop(1, fadeColor(color, 0.75));
    ctx.fillStyle = grad;
    ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2);
    ctx.fill();
    ctx.shadowBlur = 0;
  }

  // White border
  ctx.shadowBlur = 0;
  ctx.globalAlpha = highlighted ? 0.7 : 0.06;
  ctx.strokeStyle = '#fff';
  ctx.lineWidth = 2 / globalScale;
  ctx.beginPath(); ctx.arc(x, y, R, 0, Math.PI * 2);
  ctx.stroke();

  // Label with pill background
  const label = truncateLabel(node.title, globalScale > 0.7 ? 8 : 4);
  if (label && globalScale > 0.5) {
    const fs = Math.max(9, 11 / globalScale);
    ctx.font = `600 ${fs}px "PingFang SC","Microsoft YaHei",sans-serif`;
    const tw = ctx.measureText(label).width;
    const pad = 4 / globalScale;
    const lx = x - tw / 2 - pad;
    const ly = y + R + 2 / globalScale;
    const lw = tw + pad * 2;
    const lh = fs + pad;

    // Pill background
    ctx.fillStyle = 'rgba(0,0,0,0.55)';
    const bx = x - lw / 2, by = ly;
    ctx.beginPath();
    ctx.moveTo(bx + lh / 3, by);
    ctx.lineTo(bx + lw - lh / 3, by);
    ctx.quadraticCurveTo(bx + lw, by, bx + lw, by + lh / 3);
    ctx.lineTo(bx + lw, by + lh * 2 / 3);
    ctx.quadraticCurveTo(bx + lw, by + lh, bx + lw - lh / 3, by + lh);
    ctx.lineTo(bx + lh / 3, by + lh);
    ctx.quadraticCurveTo(bx, by + lh, bx, by + lh * 2 / 3);
    ctx.lineTo(bx, by + lh / 3);
    ctx.quadraticCurveTo(bx, by, bx + lh / 3, by);
    ctx.closePath();
    ctx.fill();

    // Text
    ctx.fillStyle = '#fff';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(label, x, ly + lh * 0.45);
  }

  ctx.restore();
  ctx.globalAlpha = 1;
}

/* ─── Matrix View ─── */

function MatrixView({ nodes, edges }: { nodes: WikiGraphNode[]; edges: WikiGraphEdge[] }) {
  const types = [...new Set(nodes.map((n) => n.group))].sort();
  const idx = Object.fromEntries(types.map((t, i) => [t, i]));
  const N = types.length;
  const mat: number[][] = Array.from({ length: N }, () => Array(N).fill(0));
  let maxV = 0;
  for (const e of edges) {
    const s = nodes.find((n) => n.id === e.source)?.group;
    const t = nodes.find((n) => n.id === e.target)?.group;
    if (s && t && idx[s] !== undefined && idx[t] !== undefined) {
      mat[idx[s]][idx[t]] += e.weight || 1;
      maxV = Math.max(maxV, mat[idx[s]][idx[t]]);
    }
  }
  const cs = Math.min(60, Math.max(36, 400 / N));
  return (
    <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%', padding: 24 }}>
      <div>
        <div style={{ marginBottom: 16, fontWeight: 600, fontSize: 15, textAlign: 'center' }}>
          类型引用矩阵 <span style={{ fontWeight: 400, fontSize: 12, color: 'var(--aim-text-tertiary)' }}>色深=引用密度</span>
        </div>
        <div style={{ position: 'relative', paddingLeft: 100 }}>
          {types.map((t, i) => (
            <div key={t} style={{ position: 'absolute', top: -22, left: 100 + i * cs + cs / 2, transform: 'translateX(-50%)', fontSize: 11, whiteSpace: 'nowrap', color: TYPE_META[t]?.color || '#888' }}>
              {TYPE_META[t]?.label || t}
            </div>
          ))}
          {types.map((rt, ri) => (
            <div key={rt} style={{ display: 'flex', alignItems: 'center', marginBottom: 2 }}>
              <div style={{ width: 96, textAlign: 'right', paddingRight: 8, fontSize: 11, color: TYPE_META[rt]?.color || '#888' }}>
                {TYPE_META[rt]?.label || rt}
              </div>
              {types.map((_, ci) => {
                const v = mat[ri][ci];
                const p = maxV > 0 ? v / maxV : 0;
                return (
                  <Tooltip key={ci} title={`${TYPE_META[rt]?.label || rt}→${TYPE_META[types[ci]]?.label || types[ci]}: ${v}`}>
                    <div style={{ width: cs, height: cs, margin: 1, borderRadius: 4, background: v > 0 ? `rgba(166,108,255,${0.1 + p * 0.7})` : 'var(--aim-bg-tertiary,#f5f5f5)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 10, color: p > 0.5 ? '#fff' : 'transparent', cursor: 'pointer', border: '1px solid var(--aim-border)' }}>{v > 0 ? v : ''}</div>
                  </Tooltip>
                );
              })}
            </div>
          ))}
        </div>
        <div style={{ marginTop: 16, fontSize: 12, textAlign: 'center', color: 'var(--aim-text-tertiary)' }}>{nodes.length} 节点 / {edges.length} 边</div>
      </div>
    </div>
  );
}

/* ─── Main Component ─── */

export function WikiGraphPage() {
  const { kbId } = useParams<{ kbId: string }>();
  const navigate = useNavigate();
  const containerRef = useRef<HTMLDivElement>(null);
  const fgRef = useRef<any>(null);
  const [dims, setDims] = useState({ w: 0, h: 0 });
  const [enabledTypes, setEnabledTypes] = useState<Set<string>>(new Set(NODE_TYPES));
  const [hoveredNode, setHoveredNode] = useState<WikiGraphNode | null>(null);
  const [selectedNode, setSelectedNode] = useState<WikiGraphNode | null>(null);
  const [searchText, setSearchText] = useState('');
  const [showMatrix, setShowMatrix] = useState(false);
  const [pulseTime, setPulseTime] = useState(0);

  // Pulse loop
  useEffect(() => {
    if (showMatrix) return;
    let id: number;
    const tick = () => { setPulseTime(Date.now() / 1000); id = requestAnimationFrame(tick); };
    id = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(id);
  }, [showMatrix]);

  // Graph data
  const { data: graphData, isLoading, error } = useQuery<WikiGraphData>({
    queryKey: ['wiki-graph', kbId],
    queryFn: () => kbApi.wikiGraph(kbId!),
    enabled: !!kbId,
  });

  // Resize — re-observe after data loads since the container is conditionally rendered
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => {
      if (e) setDims({ w: e.contentRect.width, h: e.contentRect.height });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [graphData]);

  // Page detail for selected node
  const pageQuery = useQuery<WikiPageRsp>({
    queryKey: ['wiki-page', kbId, selectedNode?.id],
    // Pass kbId as string to avoid JS Number precision loss
    queryFn: () => kbApi.wikiReadPage(kbId!, selectedNode!.id),
    enabled: !!kbId && !!selectedNode?.id,
  });

  // All wiki pages for slug resolution in Drawer WikiMarkdown
  const pagesQuery = useQuery({
    queryKey: ["wiki-pages", kbId],
    queryFn: () => kbApi.wikiListPages(Number(kbId!)),
    enabled: !!kbId,
  });

  const wikiPages = pagesQuery.data?.list ?? [];

  // Filtered data
  const filteredData = useMemo(() => {
    if (!graphData?.nodes) return { nodes: [], links: [] };
    const nds = graphData.nodes.filter((n) => enabledTypes.has(n.group));
    const ids = new Set(nds.map((n) => n.id));
    const edges = (graphData.edges || []).filter((e) => ids.has(e.source) && ids.has(e.target));
    return {
      nodes: nds,
      links: edges.map((e) => ({ source: e.target, target: e.source, weight: e.weight || 1 })),
    };
  }, [graphData, enabledTypes]);

  const hasSearch = searchText.trim().length > 0;

  const highlightedSet = useMemo(() => {
    // If a node is selected, highlight it and its neighbors
    if (selectedNode) {
      const neighbors = new Set<string>();
      neighbors.add(selectedNode.id);
      const edges = graphData?.edges || [];
      for (const e of edges) {
        if (e.source === selectedNode.id) neighbors.add(e.target);
        if (e.target === selectedNode.id) neighbors.add(e.source);
      }
      return neighbors;
    }
    if (hasSearch) {
      const q = searchText.toLowerCase();
      return new Set((graphData?.nodes || []).filter((n) => n.title.toLowerCase().includes(q) || n.id.toLowerCase().includes(q)).map((n) => n.id));
    }
    return null; // null = all normal brightness
  }, [searchText, graphData, selectedNode, hasSearch]);

  const isDimmed = highlightedSet !== null;
  const shouldHighlight = (id: string) => highlightedSet === null || highlightedSet.has(id);

  const linkColorFn = useCallback((l: any) => {
    const srcId = typeof l.source === 'object' ? l.source.id : l.source;
    const tgtId = typeof l.target === 'object' ? l.target.id : l.target;
    const src = graphData?.nodes?.find((n) => n.id === srcId);
    const color = src && TYPE_META[src.group] ? TYPE_META[src.group].color : '#aaa';
    if (!isDimmed) return fadeColor(color, 0.35);
    const relevant = highlightedSet!.has(srcId) && highlightedSet!.has(tgtId);
    return fadeColor(color, relevant ? 0.6 : 0.04);
  }, [graphData, highlightedSet, isDimmed]);

  const allNodes = graphData?.nodes || [];
  const nodeCanvas = useCallback(
    (node: any, ctx: CanvasRenderingContext2D, gs: number) => {
      drawNode(node, ctx, gs, allNodes, shouldHighlight(node.id), pulseTime);
    },
    [allNodes, shouldHighlight, pulseTime],
  );

  // Expanded click/drag area — multiply visual radius so the entire circle is grabbable
  const ninClickRadius = 3.5;
  const nodePointerAreaPaint = useCallback(
    (node: any, color: string, ctx: CanvasRenderingContext2D) => {
      const sz = nodeSize(node) * ninClickRadius;
      ctx.fillStyle = color;
      ctx.beginPath(); ctx.arc(node.x, node.y, sz, 0, Math.PI * 2); ctx.fill();
    },
    [],
  );

  // Clear selection with Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') setSelectedNode(null); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);

  const handleNodeClick = useCallback((node: any) => {
    setSelectedNode(node as WikiGraphNode);
  }, []);

  const zoomTo = (f: number) => fgRef.current?.zoomToFit(400, Math.max(0.2, Math.min(3, f)));
  const resetCamera = () => fgRef.current?.zoomToFit(400, 1);

  const graphReady = dims.w > 0 && dims.h > 0;

  /* Loading / Error / Empty */
  if (isLoading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: '100%', height: '100%', flex: 1 }}>
        <Spin size="large" />
      </div>
    );
  }
  if (error) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', width: '100%', flex: 1 }}>
        <div style={{ color: 'var(--aim-text-tertiary)', marginBottom: 16 }}>图谱加载失败: {(error as Error).message}</div>
        <Button onClick={() => navigate(`/knowledge/${kbId}`)} icon={<ArrowLeftOutlined />}>返回</Button>
      </div>
    );
  }
  if (!graphData?.nodes?.length) {
    return (
      <div style={{ padding: 32 }}>
        <Button onClick={() => navigate(`/knowledge/${kbId}`)} icon={<ArrowLeftOutlined />} style={{ marginBottom: 16 }}>返回</Button>
        <Empty description="暂无页面，无法生成图谱" />
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', flex: 1, overflow: 'hidden', position: 'relative', minHeight: 0 }}>
      {/* ─── Header ─── */}
      <div style={{
        padding: '8px 16px', borderBottom: '1px solid var(--aim-border)',
        display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0,
      }}>
        <Button size="small" icon={<ArrowLeftOutlined />} onClick={() => navigate(`/knowledge/${kbId}`)} />
        <span style={{ fontWeight: 600, fontSize: 14 }}>知识图谱</span>
        <span style={{ color: 'var(--aim-text-tertiary)', fontSize: 12 }}>
          {graphData.nodes.length} 节点 / {graphData.edges.length} 边
        </span>
        <div style={{ flex: 1, minWidth: 0 }} />
        <Input
          size="small" prefix={<SearchOutlined style={{ fontSize: 12 }} />}
          placeholder="搜索节点…"
          value={searchText} onChange={(e) => setSearchText(e.target.value)}
          allowClear style={{ width: 180 }}
        />
        {highlightedSet && highlightedSet.size > 0 && <Tag color="purple" style={{ fontSize: 11 }}>{highlightedSet.size}</Tag>}
        <Tooltip title="缩小"><Button size="small" icon={<ZoomOutOutlined />} onClick={() => zoomTo(0.8)} /></Tooltip>
        <Tooltip title="放大"><Button size="small" icon={<ZoomInOutlined />} onClick={() => zoomTo(1.25)} /></Tooltip>
        <Tooltip title="重置"><Button size="small" icon={<AimOutlined />} onClick={resetCamera} /></Tooltip>
        <Switch size="small" checked={showMatrix} onChange={setShowMatrix} checkedChildren={<TableOutlined />} unCheckedChildren={<ApartmentOutlined />} />
        <span style={{ fontSize: 11, color: 'var(--aim-text-tertiary)', whiteSpace: 'nowrap' }}>{showMatrix ? '矩阵' : '图谱'}</span>
      </div>

      {/* ─── Body ─── */}
      <div style={{ display: 'flex', flex: 1, overflow: 'hidden', minHeight: 0 }}>
        {!showMatrix && (
          <div style={{
            width: 120, flexShrink: 0, borderRight: '1px solid var(--aim-border)',
            padding: '12px 8px', background: 'var(--aim-surface)', overflowY: 'auto',
          }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--aim-text-secondary)', marginBottom: 8, paddingLeft: 4 }}>
              类型筛选
            </div>
            {NODE_TYPES.map((t) => {
              const cnt = graphData.nodes.filter((n) => n.group === t).length;
              const meta = TYPE_META[t];
              return (
                <div key={t} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
                  <Checkbox checked={enabledTypes.has(t)} onChange={() => {
                    setEnabledTypes((p) => { const n = new Set(p); n.has(t) ? n.delete(t) : n.add(t); return n; });
                  }} />
                  <div style={{ width: 10, height: 10, borderRadius: '50%', background: meta?.color || '#888', flexShrink: 0, boxShadow: `0 0 4px ${meta?.color}44` }} />
                  <span style={{ fontSize: 12, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {meta?.label || t}
                  </span>
                  <span style={{ fontSize: 10, color: 'var(--aim-text-tertiary)' }}>{cnt}</span>
                </div>
              );
            })}
            {filteredData.nodes.length < graphData.nodes.length && (
              <div style={{ fontSize: 10, color: 'var(--aim-text-tertiary)', paddingLeft: 4, marginTop: 4 }}>
                显示 {filteredData.nodes.length}/{graphData.nodes.length}
              </div>
            )}
          </div>
        )}

        {showMatrix ? (
          <MatrixView nodes={graphData.nodes} edges={graphData.edges} />
        ) : (
          <div ref={containerRef} style={{ flex: 1, position: 'relative', minWidth: 0 }}>
            {graphReady ? (
              <ForceGraph2D
                ref={fgRef}
                width={dims.w}
                height={dims.h}
                graphData={filteredData}
                nodeId="id"
                nodeCanvasObject={nodeCanvas}
                nodeCanvasObjectMode={() => 'replace'}
                linkSource="source"
                linkTarget="target"
                linkDirectionalArrowLength={6}
                linkDirectionalArrowRelPos={0.95}
                linkColor={linkColorFn}
                linkWidth={(l: any) => Math.min(3, Math.max(0.5, (l.weight || 1) * 0.8))}
                linkDirectionalParticles={1}
                linkDirectionalParticleSpeed={0.003}
                onEngineTick={() => {
                  const f = fgRef.current;
                  if (f) {
                    const ch = f.d3Force('charge');
                    if (ch) ch.strength((n: any) => -200 - nodeSize(n) * 5);
                    const lk = f.d3Force('link');
                    if (lk) lk.distance((l: any) => Math.min(350, Math.max(120, nodeSize(l.source) * 1.5 + nodeSize(l.target) * 1.5 + 40)));
                    const ct = f.d3Force('center');
                    if (ct) ct.strength(0.005);
                    const cl = f.d3Force('collide');
                    if (cl) cl.radius((n: any) => nodeSize(n) * 1.5);
                  }
                }}
                onNodeClick={handleNodeClick}
                onNodeHover={(n: any) => setHoveredNode(n || null)}
                nodePointerAreaPaint={nodePointerAreaPaint}
                enableNodeDrag
                enableZoomInteraction
                minZoom={0.2}
                maxZoom={3}
              />
            ) : (
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
                <Spin size="small" />
              </div>
            )}

            {/* Hover tooltip */}
            {hoveredNode && !showMatrix && (
              <div style={{
                position: 'absolute', bottom: 16, left: '50%', transform: 'translateX(-50%)',
                background: 'rgba(0,0,0,0.85)', backdropFilter: 'blur(8px)',
                color: '#fff', padding: '10px 16px', borderRadius: 10,
                fontSize: 13, pointerEvents: 'none', maxWidth: 360, whiteSpace: 'nowrap',
                boxShadow: '0 4px 20px rgba(0,0,0,0.3)',
              }}>
                <Tag color={TYPE_META[hoveredNode.group]?.color || 'default'} style={{ marginRight: 6, fontSize: 10 }}>
                  {TYPE_META[hoveredNode.group]?.label || hoveredNode.group}
                </Tag>
                <strong>{hoveredNode.title}</strong>
                <span style={{ marginLeft: 8, fontSize: 11, color: 'rgba(255,255,255,0.5)' }}>
                  被引 {hoveredNode.citation_count}
                </span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* ─── Node Detail Drawer ─── */}
      <Drawer
        title={
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Badge color={TYPE_META[selectedNode?.group || '']?.color} />
            <span style={{ fontWeight: 600 }}>{selectedNode?.title}</span>
            <Tag color={TYPE_META[selectedNode?.group || '']?.color} style={{ marginLeft: 4 }}>
              {TYPE_META[selectedNode?.group || '']?.label}
            </Tag>
          </div>
        }
        placement="right"
        onClose={() => setSelectedNode(null)}
        open={!!selectedNode}
        width={420}
        extra={
          <Button size="small" type="primary" icon={<FileTextOutlined />} onClick={() => {
            if (selectedNode) navigate(`/knowledge/${kbId}/wiki/${selectedNode.id}`);
          }}>
            完整页面
          </Button>
        }
      >
        {pageQuery.isLoading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Spin /></div>
        ) : pageQuery.isError ? (
          <div style={{ color: 'var(--aim-text-tertiary)', textAlign: 'center', padding: 20 }}>
            加载详情失败: {(pageQuery.error as Error).message}
          </div>
        ) : pageQuery.data ? (
          <div>
            {/* Meta table */}
            <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '8px 16px', fontSize: 13, background: 'var(--aim-surface)', padding: 12, borderRadius: 8, border: '1px solid var(--aim-border)' }}>
              <span style={{ color: 'var(--aim-text-tertiary)' }}>标题</span>
              <span style={{ fontWeight: 600 }}>{pageQuery.data.title}</span>
              <span style={{ color: 'var(--aim-text-tertiary)' }}>类型</span>
              <Tag color={TYPE_META[pageQuery.data.page_type]?.color} style={{ margin: 0 }}>{TYPE_META[pageQuery.data.page_type]?.label || pageQuery.data.page_type}</Tag>
              <span style={{ color: 'var(--aim-text-tertiary)' }}>被引</span>
              <span>{selectedNode?.citation_count ?? 0}</span>
              <span style={{ color: 'var(--aim-text-tertiary)' }}>版本</span>
              <span>v{pageQuery.data.version}</span>
              <span style={{ color: 'var(--aim-text-tertiary)' }}>更新时间</span>
              <span>{pageQuery.data.updated_at ? new Date(pageQuery.data.updated_at * 1000).toLocaleString('zh-CN') : '-'}</span>
            </div>

            {pageQuery.data.summary && (
              <>
                <Divider style={{ margin: '16px 0' }} />
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text)', marginBottom: 8 }}>摘要</div>
                <div className="wiki-content" style={{ fontSize: 13, color: 'var(--aim-text-secondary)', lineHeight: 1.6 }}>
                  <WikiMarkdown content={pageQuery.data.summary} pages={wikiPages} kbId={kbId ? Number(kbId) : undefined} onNavigate={(slug) => navigate(`/knowledge/${kbId}/wiki/${slug}`)} />
                </div>
              </>
            )}

            {pageQuery.data.content && (
              <>
                <Divider style={{ margin: '16px 0' }} />
                <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--aim-text)', marginBottom: 8 }}>内容预览</div>
                <div className="wiki-content" style={{ fontSize: 13, color: 'var(--aim-text-secondary)', lineHeight: 1.6, maxHeight: 300, overflowY: 'auto' }}>
                  <WikiMarkdown content={pageQuery.data.content.slice(0, 500)} pages={wikiPages} kbId={kbId ? Number(kbId) : undefined} onNavigate={(slug) => navigate(`/knowledge/${kbId}/wiki/${slug}`)} />
                </div>
              </>
            )}

            <div style={{ marginTop: 20 }}>
              <Button icon={<LinkOutlined />} block onClick={() => navigate(`/knowledge/${kbId}/wiki/${selectedNode!.id}`)}>
                查看完整 Wiki 页面
              </Button>
            </div>
          </div>
        ) : (
          <div style={{ textAlign: 'center', padding: 20, color: 'var(--aim-text-tertiary)' }}>点击节点查看详情</div>
        )}
      </Drawer>
    </div>
  );
}
