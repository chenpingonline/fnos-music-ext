<script setup lang="ts">
import { UserRound, UsersRound, Activity, CircleHelp, RefreshCw, Plus, Music2, X, Check, TriangleAlert, ChevronRight, Search, LockKeyhole, Link, Trash2, Copy, LogOut, ListMusic, HardDrive, Download, QrCode, Box } from 'lucide-vue-next'
defineProps<{name:string}>()
const icons:Record<string,typeof Music2> = {user:UserRound,accounts:UsersRound,status:Activity,help:CircleHelp,refresh:RefreshCw,plus:Plus,music:Music2,close:X,check:Check,alert:TriangleAlert,arrow:ChevronRight,search:Search,lock:LockKeyhole,link:Link,trash:Trash2,copy:Copy,logout:LogOut,list:ListMusic,drive:HardDrive,download:Download,qr:QrCode,box:Box}
</script>
<template><component :is="icons[name] || Music2" :size="20" :stroke-width="1.7" aria-hidden="true"/></template>
