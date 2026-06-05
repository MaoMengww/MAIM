import './PresenceDot.css';

interface Props {
  online: boolean;
  size?: 'small' | 'medium';
  className?: string;
}

export function PresenceDot({ online, size = 'small', className = '' }: Props) {
  return (
    <span
      className={`presence-dot presence-dot--${size} ${online ? 'presence-dot--online' : 'presence-dot--offline'} ${className}`}
      title={online ? '在线' : '离线'}
    />
  );
}
