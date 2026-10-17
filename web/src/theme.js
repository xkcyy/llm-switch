export const uiTokens = {
  rail: '#0f1620',
  railSoft: '#151e2b',
  railLine: '#263244',
  canvas: '#f4f6f9',
  surface: '#ffffff',
  surfaceSoft: '#f8fafc',
  ink: '#151b24',
  inkSoft: '#5e6877',
  inkMuted: '#66707f',
  line: '#e3e8ef',
  lineStrong: '#cdd5df',
  blue: '#2f6bff',
  blueSoft: '#eef4ff',
  green: '#16a34a',
  greenStrong: '#15803d',
  greenSoft: '#eefaf3',
  amber: '#d97706',
  amberSoft: '#fff7ed',
  red: '#dc2626',
  redSoft: '#fef2f2',
  cyan: '#0e7490',
  violet: '#7c3aed'
}

export const appTheme = {
  token: {
    colorPrimary: uiTokens.blue,
    colorInfo: uiTokens.blue,
    colorSuccess: uiTokens.green,
    colorWarning: uiTokens.amber,
    colorError: uiTokens.red,
    colorTextBase: uiTokens.ink,
    colorBgLayout: uiTokens.canvas,
    colorBgContainer: uiTokens.surface,
    colorBorder: uiTokens.lineStrong,
    colorBorderSecondary: uiTokens.line,
    borderRadius: 10,
    borderRadiusLG: 12,
    borderRadiusSM: 8,
    controlHeight: 34,
    fontSize: 13,
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif'
  },
  components: {
    Layout: {
      siderBg: uiTokens.rail,
      headerBg: uiTokens.surface,
      bodyBg: uiTokens.canvas
    },
    Menu: {
      darkItemBg: uiTokens.rail,
      darkSubMenuItemBg: uiTokens.rail,
      darkItemColor: '#aeb8c7',
      darkItemHoverColor: '#ffffff',
      darkItemSelectedBg: uiTokens.blue,
      darkItemSelectedColor: '#ffffff',
      itemBorderRadius: 8,
      itemHeight: 38,
      itemMarginInline: 10
    },
    Card: {
      headerFontSize: 14,
      headerHeight: 46,
      paddingLG: 20
    },
    Table: {
      headerBg: uiTokens.surfaceSoft,
      headerColor: uiTokens.inkSoft,
      rowHoverBg: '#f6f8fc',
      borderColor: uiTokens.line,
      cellPaddingBlockSM: 8,
      cellPaddingInlineSM: 12
    },
    Drawer: {
      paddingLG: 20
    },
    Modal: {
      titleFontSize: 16
    },
    Segmented: {
      itemSelectedBg: uiTokens.surface,
      itemSelectedColor: uiTokens.ink
    }
  }
}
