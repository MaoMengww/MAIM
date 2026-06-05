import React from 'react';

interface IconProps {
  size?: number;
  style?: React.CSSProperties;
  className?: string;
}

export function WikiIcon({ size = 28, className, style }: IconProps) {
  return (
    <svg
      className={className}
      aria-hidden="true"
      style={{ fontSize: size, width: size, height: size, ...style }}
      viewBox="0 0 1024 1024"
      fill="currentColor"
    >
      <path d="M832 128H320c-52.8 0-96 43.2-96 96v576c0 52.8 43.2 96 96 96h512c52.8 0 96-43.2 96-96V224c0-52.8-43.2-96-96-96zM800 800H352c-17.6 0-32-14.4-32-32s14.4-32 32-32h448v64zM800 672H352c-17.6 0-32-14.4-32-32s14.4-32 32-32h448v64zM800 544H352c-17.6 0-32-14.4-32-32s14.4-32 32-32h448v64zM800 416H352c-17.6 0-32-14.4-32-32s14.4-32 32-32h448v64z" />
      <path d="M192 768V192c0-35.2 28.8-64 64-64h512V64H224C188.8 64 160 92.8 160 128v640h32z" />
    </svg>
  );
}

export function FolderIcon({ size = 28, className, style }: IconProps) {
  return (
    <svg
      className={className}
      aria-hidden="true"
      style={{ fontSize: size, width: size, height: size, ...style }}
      viewBox="0 0 1024 1024"
      fill="currentColor"
    >
      <path d="M880 298.4H521L403.7 181.1c-5.1-5.1-12-7.9-19.2-7.9H144c-17.7 0-32 14.3-32 32v640c0 17.7 14.3 32 32 32h736c17.7 0 32-14.3 32-32V330.4c0-17.7-14.3-32-32-32z" />
    </svg>
  );
}

export function InfoIcon({ size = 12, className, style }: IconProps) {
  return (
    <svg
      className={className}
      aria-hidden="true"
      style={{ fontSize: size, width: size, height: size, verticalAlign: 'middle', ...style }}
      viewBox="0 0 1024 1024"
      fill="currentColor"
    >
      <path d="M512 64C264.6 64 64 264.6 64 512s200.6 448 448 448 448-200.6 448-448S759.4 64 512 64zM512 768c-17.7 0-32-14.3-32-32V480c0-17.7 14.3-32 32-32s32 14.3 32 32v256c0 17.7-14.3 32-32 32zM544 368c0 17.7-14.3 32-32 32s-32-14.3-32-32 14.3-32 32-32 32 14.3 32 32z" />
    </svg>
  );
}

export function EditIcon({ size = 14, className, style }: IconProps) {
  return (
    <svg
      className={className}
      aria-hidden="true"
      style={{ fontSize: size, width: size, height: size, verticalAlign: 'middle', ...style }}
      viewBox="0 0 1024 1024"
      fill="currentColor"
    >
      <path d="M880 836H144c-17.7 0-32 14.3-32 32v36c0 4.4 3.6 8 8 8h784c4.4 0 8-3.6 8-8v-36c0-17.7-14.3-32-32-32zM652.4 182.2l-543 543c-6.1 6.1-8.9 14.3-8 22.5l5.6 61.6c1.2 13.2 11.6 23.6 24.8 24.8l61.6 5.6c8.2 0.7 16.4-1.9 22.5-8l543-543c25-25 25-65.5 0-90.5l-106.4-106.4c-25-25-65.5-25-90.5 0z" />
    </svg>
  );
}
