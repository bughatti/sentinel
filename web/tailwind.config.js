/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{svelte,ts,js}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        surface: {
          DEFAULT: '#0f0f0f',
          1: '#161616',
          2: '#1e1e1e',
          3: '#262626',
          4: '#303030',
        },
        accent: {
          DEFAULT: '#3b82f6',
          hover: '#2563eb',
        },
        label: {
          person: '#3b82f6',
          car: '#eab308',
          dog: '#22c55e',
          cat: '#a855f7',
          bike: '#f97316',
          truck: '#f59e0b',
          motorcycle: '#ef4444',
          default: '#6b7280',
        },
      },
      animation: {
        'pulse-fast': 'pulse 1s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        'fade-in': 'fadeIn 0.2s ease-out',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0', transform: 'translateY(4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
    },
  },
  plugins: [],
}
