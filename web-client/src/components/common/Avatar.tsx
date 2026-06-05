import { useState, useMemo } from 'react';

interface AvatarProps {
  name?: string;
  src?: string;
  size?: number;
  style?: React.CSSProperties;
}

const COLORS = [
  'linear-gradient(135deg, #FF7D4A, #FF5E62)',
  'linear-gradient(135deg, #4ECDC4, #44B0A8)',
  'linear-gradient(135deg, #60A5FA, #3B82F6)',
  'linear-gradient(135deg, #A78BFA, #8B5CF6)',
  'linear-gradient(135deg, #FBBF24, #F59E0B)',
  'linear-gradient(135deg, #FB7185, #F43F5E)',
  'linear-gradient(135deg, #34D399, #10B981)',
  'linear-gradient(135deg, #F472B6, #EC4899)',
];

function hashColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  }
  return COLORS[Math.abs(hash) % COLORS.length];
}

function initials(name?: string): string {
  if (!name) return '?';
  const words = name.trim().split(/\s+/);
  if (words.length >= 2) {
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  return name.slice(0, 2).toUpperCase();
}

export function Avatar({ name, src, size = 40, style }: AvatarProps) {
  const [imgError, setImgError] = useState(false);
  const bg = useMemo(() => hashColor(name || '?'), [name]);

  if (src && !imgError) {
    return (
      <img
        src={src}
        alt={name || 'avatar'}
        style={{
          width: size,
          height: size,
          borderRadius: '50%',
          objectFit: 'cover',
          ...style,
        }}
        onError={() => setImgError(true)}
      />
    );
  }

  return (
    <div
      style={{
        width: size,
        height: size,
        borderRadius: '50%',
        background: bg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        color: '#fff',
        fontSize: size * 0.4,
        fontWeight: 600,
        flexShrink: 0,
        ...style,
      }}
    >
      {initials(name)}
    </div>
  );
}
